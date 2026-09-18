package identity

import (
	"context"
	"errors"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/session"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	kerrors "github.com/go-kratos/kratos/v3/errors"
)

func TestChangeOwnPasswordInvalidatesOldSessions(t *testing.T) {
	repo, uc, d := newAdminResetFixture(t)
	ctx := context.Background()
	id := seedAdmin(t, d)
	old := d.Client.AdminUser.GetX(ctx, id)
	login, err := uc.StartLogin(ctx, "victim", "oldpass123", "")
	if err != nil {
		t.Fatal(err)
	}
	seedSession(t, d, id, session.RealmUser)
	seedSession(t, d, id+1, session.RealmAdmin)
	svc := NewAdminAuthService(uc, nil, nil)
	request := &adminv1.ChangeAdminPasswordRequest{CurrentPassword: "oldpass123", NewPassword: "newpass456", ConfirmPassword: "newpass456"}
	if _, err = svc.ChangePassword(ctx, request); kerrors.Code(err) != 401 {
		t.Fatalf("anonymous: %v", err)
	}
	claims, err := uc.signer.Verify(authn.RealmAdmin, login.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ChangePassword(WithClaims(ctx, claims), request); err != nil {
		t.Fatal(err)
	}
	now := d.Client.AdminUser.GetX(ctx, id)
	if !crypto.VerifyPassword(now.PasswordHash, "newpass456") || crypto.VerifyPassword(now.PasswordHash, "oldpass123") || now.AuthVersion != old.AuthVersion+1 {
		t.Fatal("password/version not updated")
	}
	acc, err := repo.Admin(ctx, id)
	if err != nil || claims.AuthVersion == acc.AuthVersion {
		t.Fatal("old access token still has current auth version")
	}
	if _, err = uc.RefreshAccess(ctx, login.RefreshToken); err == nil {
		t.Fatal("old refresh still accepted")
	}
	if _, err = uc.StartLogin(ctx, "victim", "oldpass123", ""); !errors.Is(err, ErrLoginFailed) {
		t.Fatal("old password accepted", err)
	}
	if _, err = uc.StartLogin(ctx, "victim", "newpass456", ""); err != nil {
		t.Fatal("new password rejected", err)
	}
	for _, s := range d.Client.Session.Query().AllX(ctx) {
		if (s.Realm == session.RealmUser || s.UserID != id) && !s.RevokedAt.IsZero() {
			t.Fatal("unrelated session revoked")
		}
	}
	if _, err = svc.ChangePassword(WithClaims(ctx, claims), request); kerrors.Code(err) != 401 {
		t.Fatal("stale token accepted", err)
	}
}

func TestChangeOwnPasswordValidationAndRollback(t *testing.T) {
	_, uc, d := newAdminResetFixture(t)
	ctx := context.Background()
	id := seedAdmin(t, d)
	initial := d.Client.AdminUser.GetX(ctx, id)
	for _, args := range [][3]string{{"", "newpass456", "newpass456"}, {"oldpass123", "short", "short"}, {"oldpass123", "newpass456", "different"}, {"oldpass123", "oldpass123", "oldpass123"}, {"oldpass123", strings.Repeat("长", 25), strings.Repeat("长", 25)}} {
		if err := uc.ChangeOwnPassword(ctx, id, initial.AuthVersion, args[0], args[1], args[2]); !errors.Is(err, ErrPasswordInput) {
			t.Fatal(args[0] == "", err)
		}
	}
	if err := uc.ChangeOwnPassword(ctx, id, initial.AuthVersion, "incorrect", "newpass456", "newpass456"); !errors.Is(err, ErrLoginFailed) {
		t.Fatal(err)
	}
	unchanged, state := mfaRow(t, d, id)
	if unchanged.PasswordHash != initial.PasswordHash || unchanged.AuthVersion != initial.AuthVersion || state.PasswordFails != 1 {
		t.Fatal("failed verification changed account or lost rate limit")
	}
	seedSession(t, d, id, session.RealmAdmin)
	_, err := d.DB.Exec(`CREATE TRIGGER fail_password_audit BEFORE INSERT ON security_audit_logs BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = uc.ChangeOwnPassword(ctx, id, initial.AuthVersion, "oldpass123", "newpass456", "newpass456"); err == nil {
		t.Fatal("partial commit")
	}
	row := d.Client.AdminUser.GetX(ctx, id)
	if row.PasswordHash != initial.PasswordHash || row.AuthVersion != initial.AuthVersion {
		t.Fatal("password transaction failed to roll back")
	}
	if !d.Client.Session.Query().OnlyX(ctx).RevokedAt.IsZero() {
		t.Fatal("session invalidation failed to roll back")
	}
}

func TestChangeOwnPasswordKeepsBoundMFA(t *testing.T) {
	_, uc, d := newAdminResetFixture(t)
	ctx := context.Background()
	id := seedAdmin(t, d)
	old := d.Client.AdminUser.GetX(ctx, id)
	bindMFA(t, uc, id, old.AuthVersion)
	before, state := mfaRow(t, d, id)
	if err := uc.ChangeOwnPassword(ctx, id, before.AuthVersion, "oldpass123", "newpass456", "newpass456"); err != nil {
		t.Fatal(err)
	}
	after, got := mfaRow(t, d, id)
	if string(before.TotpSecret) != string(after.TotpSecret) || len(got.Recovery) != len(state.Recovery) {
		t.Fatal("bound authenticator changed")
	}
	result, err := uc.StartLogin(ctx, "victim", "newpass456", "")
	if err != nil || result.Challenge == "" || result.AccessToken != "" {
		t.Fatal("MFA bypass after password change", err)
	}
}
