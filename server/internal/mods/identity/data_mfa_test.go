package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/session"
	"github.com/NovaWorks/zcard-next/server/internal/mods/captcha"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/pquerna/otp/totp"
)

func otpAt(t *testing.T, secret string, offset int) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now().Add(time.Duration(offset)*30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return code
}
func mfaRow(t *testing.T, d *data.Data, id uint64) (*ent.AdminUser, mfaState) {
	t.Helper()
	u, err := d.Client.AdminUser.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var s mfaState
	if err = json.Unmarshal([]byte(u.MfaState), &s); err != nil {
		t.Fatal(err)
	}
	return u, s
}
func bindMFA(t *testing.T, uc *IdentityUsecase, id uint64, version int) (string, []string) {
	t.Helper()
	ctx := context.Background()
	secret, _, err := uc.SetupMFA(ctx, id, version, "oldpass123", "", "")
	if err != nil {
		t.Fatal(err)
	}
	codes, err := uc.ConfirmMFA(ctx, id, version, otpAt(t, secret, -1))
	if err != nil {
		t.Fatal(err)
	}
	return secret, codes
}

func TestMFALifecycle(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var uc *IdentityUsecase
			var d *data.Data
			if driver == "sqlite" {
				_, uc, d = newAdminResetFixture(t)
				d.DB.SetMaxOpenConns(1)
			} else {
				source := os.Getenv("ZCARD_MFA_" + strings.ToUpper(driver) + "_DSN")
				if source == "" {
					t.Skip("no isolated test database configured")
				}
				var cleanup func()
				var err error
				d, cleanup, err = data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: driver, Source: source}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(cleanup)
				if err = d.Client.Schema.Create(context.Background()); err != nil {
					t.Fatal(err)
				}
				// Test-only databases. Callers opt in explicitly through dedicated variables.
				if _, err = d.Client.AdminUser.Delete().Exec(context.Background()); err != nil {
					t.Fatal(err)
				}
				signer, _ := authn.NewSigner(make([]byte, 32), make([]byte, 32), time.Hour)
				box, _ := crypto.NewBox(make([]byte, 32))
				uc = NewIdentityUsecase(NewAdminUserRepoImpl(d), signer, d, box)
			}
			ctx := context.Background()
			id := seedAdmin(t, d)
			login, err := uc.StartLogin(ctx, "victim", "oldpass123", "127.0.0.1")
			if err != nil || login.AccessToken == "" {
				t.Fatal(login, err)
			}
			secret, _, err := uc.SetupMFA(ctx, id, 0, "oldpass123", "", "")
			if err != nil {
				t.Fatal(err)
			}
			u, state := mfaRow(t, d, id)
			if len(u.TotpSecret) != 0 || len(state.Pending) == 0 {
				t.Fatal("setup prematurely enables MFA")
			}
			if _, err = uc.StartLogin(ctx, "victim", "oldpass123", "127.0.0.1"); err != nil {
				t.Fatal("pending blocks login", err)
			}
			if _, err = uc.ConfirmMFA(ctx, id, 0, "bad"); !errors.Is(err, ErrTOTPInvalid) {
				t.Fatal(err)
			}
			codes, err := uc.ConfirmMFA(ctx, id, 0, otpAt(t, secret, -1))
			if err != nil {
				t.Fatal(err)
			}
			if len(codes) != 10 {
				t.Fatal("recovery count", len(codes))
			}
			u, state = mfaRow(t, d, id)
			if u.AuthVersion != 1 || len(state.Pending) != 0 {
				t.Fatal("activation state")
			}
			for _, code := range codes {
				if strings.Contains(u.MfaState, code) {
					t.Fatal("plaintext recovery code in database")
				}
			}
			if strings.Contains(u.MfaState, secret) {
				t.Fatal("plaintext secret in database")
			}
			if _, err = uc.RefreshAccess(ctx, login.RefreshToken); !errors.Is(err, ErrSessionInvalid) {
				t.Fatal("old refresh survives binding", err)
			}
			claims, err := uc.signer.Verify(authn.RealmAdmin, login.AccessToken)
			if err != nil || claims.AuthVersion == u.AuthVersion {
				t.Fatal("old access version still valid")
			}
			challenge, err := uc.StartLogin(ctx, "victim", "oldpass123", "127.0.0.1")
			if err != nil || challenge.Challenge == "" || challenge.AccessToken != "" || challenge.RefreshToken != "" {
				t.Fatal("challenge must not issue tokens", err)
			}
			if _, err = uc.FinishLogin(ctx, challenge.Challenge, "bad", "127.0.0.1"); !errors.Is(err, ErrTOTPInvalid) {
				t.Fatal(err)
			}
			loginCode := otpAt(t, secret, 0)
			done, err := uc.FinishLogin(ctx, challenge.Challenge, loginCode, "127.0.0.1")
			if err != nil || done.AccessToken == "" {
				t.Fatal(err)
			}
			if _, err = uc.FinishLogin(ctx, challenge.Challenge, codes[0], "127.0.0.1"); !errors.Is(err, ErrMFAExpired) {
				t.Fatal("challenge replay", err)
			}
			challenge, _ = uc.StartLogin(ctx, "victim", "oldpass123", "127.0.0.1")
			if _, err = uc.FinishLogin(ctx, challenge.Challenge, loginCode, "127.0.0.1"); !errors.Is(err, ErrTOTPInvalid) {
				t.Fatal("OTP replay", err)
			}
			recovered, err := uc.FinishLogin(ctx, challenge.Challenge, codes[0], "127.0.0.1")
			if err != nil || recovered.RecoveryTicket == "" {
				t.Fatal("recovery login", err)
			}
			fresh, _, err := uc.SetupMFA(ctx, id, 1, "oldpass123", "", recovered.RecoveryTicket)
			if err != nil {
				t.Fatal("lost phone cannot rebind", err)
			}
			u, _ = mfaRow(t, d, id)
			if len(u.TotpSecret) == 0 {
				t.Fatal("old binding removed before confirmation")
			}
			if _, _, err = uc.SetupMFA(ctx, id, 1, "oldpass123", "", recovered.RecoveryTicket); !errors.Is(err, ErrTOTPInvalid) {
				t.Fatal("recovery ticket replay", err)
			}
			newCodes, err := uc.ConfirmMFA(ctx, id, 1, otpAt(t, fresh, -1))
			if err != nil {
				t.Fatal(err)
			}
			challenge, _ = uc.StartLogin(ctx, "victim", "oldpass123", "127.0.0.1")
			if _, err = uc.FinishLogin(ctx, challenge.Challenge, codes[1], "127.0.0.1"); !errors.Is(err, ErrTOTPInvalid) {
				t.Fatal("old recovery code survives replacement", err)
			}
			logged, err := uc.FinishLogin(ctx, challenge.Challenge, newCodes[0], "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			// Same refresh/challenge can succeed at most once even on multiple workers.
			var successes atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := uc.RefreshAccess(ctx, logged.RefreshToken); err == nil {
						successes.Add(1)
					}
				}()
			}
			wg.Wait()
			if successes.Load() != 1 {
				t.Fatalf("refresh consumed %d times", successes.Load())
			}
			pendingSecret, _, err := uc.SetupMFA(ctx, id, 2, "oldpass123", newCodes[1], "")
			if err != nil {
				t.Fatal(err)
			}
			stale, _ := uc.StartLogin(ctx, "victim", "oldpass123", "127.0.0.1")
			// Offline reset requires no cipher and preserves disabled status/password/role.
			if err = d.Client.AdminUser.UpdateOneID(id).SetEnabled(false).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if err = ResetAdminTwoFactor(ctx, d, id, 0, "test offline recovery"); err != nil {
				t.Fatal(err)
			}
			u, state = mfaRow(t, d, id)
			if u.Enabled || u.RoleID != 1 || len(u.TotpSecret) != 0 || len(state.Pending) != 0 || len(state.Recovery) != 0 || state.Challenge != "" {
				t.Fatal("reset state")
			}
			if _, err = uc.ConfirmMFA(ctx, id, 2, otpAt(t, pendingSecret, 0)); err == nil {
				t.Fatal("stale setup reenabled MFA")
			}
			if err = d.Client.AdminUser.UpdateOneID(id).SetEnabled(true).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = uc.FinishLogin(ctx, stale.Challenge, newCodes[2], "127.0.0.1"); !errors.Is(err, ErrMFAExpired) {
				t.Fatal("stale challenge survives", err)
			}
			if _, err = uc.StartLogin(ctx, "victim", "oldpass123", "127.0.0.1"); err != nil {
				t.Fatal("reset cannot login", err)
			}
			if err = ResetAdminTwoFactor(ctx, d, id, 0, "repeat"); err != nil {
				t.Fatal("reset not idempotent", err)
			}
		})
	}
}

func TestMFALimitsExpiryAndRollback(t *testing.T) {
	_, uc, d := newAdminResetFixture(t)
	d.DB.SetMaxOpenConns(1)
	ctx := context.Background()
	id := seedAdmin(t, d)
	_, codes := bindMFA(t, uc, id, 0)
	for i := 0; i < 5; i++ {
		c, err := uc.StartLogin(ctx, "victim", "oldpass123", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = uc.FinishLogin(ctx, c.Challenge, "bad", ""); !errors.Is(err, ErrTOTPInvalid) {
			t.Fatal(err)
		}
	}
	// A fresh usecase/process sees the same persistent lock.
	other := NewIdentityUsecase(uc.repo, uc.signer, d, uc.cipher)
	if _, err := other.StartLogin(ctx, "victim", "oldpass123", ""); !errors.Is(err, ErrLocked) {
		t.Fatal("lock lost after restart", err)
	}
	if err := ResetAdminTwoFactor(ctx, d, id, 0, "unlock factor failures"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.StartLogin(ctx, "victim", "oldpass123", ""); err != nil {
		t.Fatal("CLI did not clear factor lock", err)
	}
	// Pending cancellation and expiry never activate credentials.
	u, _ := mfaRow(t, d, id)
	secret, _, err := uc.SetupMFA(ctx, id, u.AuthVersion, "oldpass123", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = uc.CancelMFA(ctx, id, u.AuthVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = uc.ConfirmMFA(ctx, id, u.AuthVersion, otpAt(t, secret, 0)); !errors.Is(err, ErrMFAExpired) {
		t.Fatal(err)
	}
	_, _, err = uc.SetupMFA(ctx, id, u.AuthVersion, "oldpass123", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = mfaChange(ctx, d, id, func(_ context.Context, _ *ent.AdminUser, s *mfaState) error { s.PendingUntil = 1; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err = uc.ConfirmMFA(ctx, id, u.AuthVersion, "000000"); !errors.Is(err, ErrMFAExpired) {
		t.Fatal(err)
	}
	// Force a transaction failure after writes; all credential changes roll back.
	before, _ := mfaRow(t, d, id)
	sentinel := errors.New("forced storage failure")
	err = mfaChange(ctx, d, id, func(ctx context.Context, u *ent.AdminUser, s *mfaState) error {
		u.AuthVersion++
		s.Recovery = []string{codes[0]}
		if err := data.Client(ctx, d).Session.Update().Where(session.UserID(id)).SetRevokedAt(time.Now()).Exec(ctx); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	after, _ := mfaRow(t, d, id)
	if before.AuthVersion != after.AuthVersion || before.MfaRevision != after.MfaRevision || before.MfaState != after.MfaState {
		t.Fatal("failed transaction partially persisted")
	}
	for i := 0; i < 30; i++ {
		if err := uc.CheckLoginIP(ctx, "192.0.2.1"); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := other.CheckLoginIP(ctx, "192.0.2.1"); !errors.Is(err, ErrIPLimited) {
		t.Fatal("IP limit missing", err)
	}
	if err := other.CheckLoginIP(ctx, "192.0.2.2"); err != nil {
		t.Fatal("unrelated IP locked", err)
	}
}

func TestMFAStepLeadingZeroAndWindow(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	now := time.Unix(1111111109, 0)
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if matchStep(secret, code, now) != now.Unix()/30 {
		t.Fatal("valid time step rejected")
	}
	if matchStep(secret, code, now.Add(90*time.Second)) >= 0 {
		t.Fatal("accepts excessive drift")
	}
	for i := int64(1); i < 1000; i++ {
		at := time.Unix(i*30, 0)
		code, _ := totp.GenerateCode(secret, at)
		if strings.HasPrefix(code, "0") {
			if matchStep(secret, code, at) != i {
				t.Fatal("leading zero")
			}
			if matchStep(secret, code[1:], at) >= 0 {
				t.Fatal("short code accepted")
			}
			return
		}
	}
	t.Fatal(fmt.Sprint("no leading-zero vector found"))
}

type mfaCaptchaSettings struct{}

func (mfaCaptchaSettings) GetJSON(context.Context, string, string) ([]byte, error) {
	return []byte("true"), nil
}
func TestMFALoginCaptchaBoundary(t *testing.T) {
	_, uc, d := newAdminResetFixture(t)
	ctx := context.Background()
	id := seedAdmin(t, d)
	_, codes := bindMFA(t, uc, id, 0)
	svc := NewAdminAuthService(uc, nil, captcha.New(mfaCaptchaSettings{}))
	if _, err := svc.Login(ctx, &adminv1.LoginRequest{Username: "victim", Password: "oldpass123"}); err == nil {
		t.Fatal("password phase bypassed captcha")
	}
	challenge, err := uc.StartLogin(ctx, "victim", "oldpass123", "")
	if err != nil {
		t.Fatal(err)
	}
	reply, err := svc.Login(ctx, &adminv1.LoginRequest{Challenge: challenge.Challenge, TotpCode: codes[0]})
	if err != nil || reply.GetAccessToken() == "" {
		t.Fatal("second phase incorrectly consumes captcha again", err)
	}
}
func TestMFAResetRequiresOperatorReauthentication(t *testing.T) {
	_, uc, d := newAdminResetFixture(t)
	ctx := context.Background()
	target := seedAdmin(t, d)
	_, _ = bindMFA(t, uc, target, 0)
	hash, _ := crypto.HashPassword("oldpass123")
	actor, err := d.Client.AdminUser.Create().SetUsername("operator").SetPasswordHash(hash).SetRoleID(1).SetEnabled(true).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, codes := bindMFA(t, uc, actor.ID, 0)
	if err = uc.ResetOtherMFA(ctx, actor.ID, target, 1, "wrong", codes[0], "lost phone"); !errors.Is(err, ErrLoginFailed) {
		t.Fatal(err)
	}
	if err = uc.ResetOtherMFA(ctx, actor.ID, target, 1, "oldpass123", "bad", "lost phone"); !errors.Is(err, ErrTOTPInvalid) {
		t.Fatal(err)
	}
	u, _ := mfaRow(t, d, target)
	if len(u.TotpSecret) == 0 {
		t.Fatal("reset without operator proof")
	}
	if err = uc.ResetOtherMFA(ctx, actor.ID, target, 1, "oldpass123", codes[0], "lost phone"); err != nil {
		t.Fatal(err)
	}
	u, _ = mfaRow(t, d, target)
	if len(u.TotpSecret) > 0 || u.AuthVersion != 2 {
		t.Fatal("verified reset failed")
	}
}
