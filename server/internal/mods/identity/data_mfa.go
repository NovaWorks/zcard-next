package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/adminuser"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/risklockkey"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/securityauditlog"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/session"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

var ErrIPLimited = errors.New("identity.IP_LIMITED")
var ErrMFAExpired = errors.New("identity.MFA_EXPIRED")
var ErrMFAConflict = errors.New("identity.MFA_CONFLICT")

// State is stored with an optimistic revision. Secrets are encrypted; bearer
// challenges, recovery tickets and recovery codes are stored as hashes only.
type mfaState struct {
	Pending        []byte   `json:"pending,omitempty"`
	PendingUntil   int64    `json:"pending_until,omitempty"`
	BoundAt        int64    `json:"bound_at,omitempty"`
	LastStep       int64    `json:"last_step,omitempty"`
	Recovery       []string `json:"recovery,omitempty"`
	Challenge      string   `json:"challenge,omitempty"`
	ChallengeUntil int64    `json:"challenge_until,omitempty"`
	Ticket         string   `json:"ticket,omitempty"`
	TicketUntil    int64    `json:"ticket_until,omitempty"`
	PasswordFails  int      `json:"password_fails,omitempty"`
	PasswordUntil  int64    `json:"password_until,omitempty"`
	FactorFails    int      `json:"factor_fails,omitempty"`
	FactorUntil    int64    `json:"factor_until,omitempty"`
}

// Expected authentication failures must commit their counters. Infrastructure
// errors roll back everything, including the revision and audit entry.
func mfaChange(ctx context.Context, d *data.Data, id uint64, fn func(context.Context, *ent.AdminUser, *mfaState) error) error {
	var result error
	err := data.Tx(ctx, d, func(ctx context.Context) error {
		c := data.Client(ctx, d)
		u, err := c.AdminUser.Get(ctx, id)
		if err != nil {
			return err
		}
		n, err := c.AdminUser.Update().Where(adminuser.ID(id), adminuser.MfaRevision(u.MfaRevision)).AddMfaRevision(1).Save(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrMFAConflict
		}
		state := &mfaState{}
		if err = json.Unmarshal([]byte(u.MfaState), state); err != nil {
			return err
		}
		result = fn(ctx, u, state)
		if result != nil && !errors.Is(result, ErrLoginFailed) && !errors.Is(result, ErrTOTPInvalid) && !errors.Is(result, ErrLocked) && !errors.Is(result, ErrMFAExpired) && !errors.Is(result, ErrTOTPRequired) {
			return result
		}
		raw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		q := c.AdminUser.UpdateOneID(id).SetMfaState(string(raw)).SetAuthVersion(u.AuthVersion)
		if len(u.TotpSecret) == 0 {
			q.ClearTotpSecret()
		} else {
			q.SetTotpSecret(u.TotpSecret)
		}
		return q.Exec(ctx)
	})
	if err != nil {
		return err
	}
	return result
}
func checkPassword(u *ent.AdminUser, s *mfaState, password string) error {
	now := time.Now().Unix()
	if !u.Enabled {
		return ErrAdminDisabled
	}
	if s.PasswordUntil <= now {
		s.PasswordFails = 0
		s.PasswordUntil = 0
	}
	if s.PasswordFails >= lockThreshold {
		return ErrLocked
	}
	if !crypto.VerifyPassword(u.PasswordHash, password) {
		s.PasswordFails++
		s.PasswordUntil = now + int64(lockDuration.Seconds())
		return ErrLoginFailed
	}
	s.PasswordFails = 0
	s.PasswordUntil = 0
	return nil
}
func factorAllowed(s *mfaState) error {
	if s.FactorUntil <= time.Now().Unix() {
		s.FactorFails = 0
		s.FactorUntil = 0
	}
	if s.FactorFails >= lockThreshold {
		return ErrLocked
	}
	return nil
}
func factorFail(s *mfaState) error {
	s.FactorFails++
	s.FactorUntil = time.Now().Add(lockDuration).Unix()
	return ErrTOTPInvalid
}
func matchStep(secret, code string, now time.Time) int64 {
	if len(code) != 6 {
		return -1
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return -1
		}
	}
	step := now.Unix() / 30
	for _, offset := range []int64{0, -1, 1} {
		candidate := step + offset
		want, err := totp.GenerateCodeCustom(secret, time.Unix(candidate*30, 0), totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err == nil && want == code {
			return candidate
		}
	}
	return -1
}
func (uc *IdentityUsecase) factor(u *ent.AdminUser, s *mfaState, code string) (bool, error) {
	if err := factorAllowed(s); err != nil {
		return false, err
	}
	if len(u.TotpSecret) == 0 {
		return false, ErrTOTPRequired
	}
	hash := hashToken(strings.ToUpper(strings.TrimSpace(code)))
	for i, h := range s.Recovery {
		if h == hash {
			s.Recovery = append(s.Recovery[:i], s.Recovery[i+1:]...)
			s.FactorFails = 0
			return true, nil
		}
	}
	secret, err := uc.cipher.Open(u.TotpSecret, []byte(fmt.Sprintf("totp:%d", u.ID)))
	if err != nil {
		return false, factorFail(s)
	}
	step := matchStep(string(secret), code, time.Now())
	if step < 0 || step <= s.LastStep {
		return false, factorFail(s)
	}
	s.LastStep = step
	s.FactorFails = 0
	s.FactorUntil = 0
	return false, nil
}
func mfaAudit(ctx context.Context, d *data.Data, actor, target uint64, action, reason string) error {
	actorType := securityauditlog.ActorTypeAdmin
	if actor == 0 {
		actorType = securityauditlog.ActorTypeSystem
	}
	if err := data.Client(ctx, d).SecurityAuditLog.Create().SetActorType(actorType).SetActorID(actor).SetAction(action).SetMetadata(map[string]any{"target_admin_id": target, "reason": reason}).Exec(ctx); err != nil {
		return err
	}
	if action != "identity.totp_bound" && action != "identity.totp_disabled" && action != "identity.totp_reset" {
		return nil
	}
	payload, err := json.Marshal(map[string]any{"admin_id": target, "operator_id": actor, "action": action, "at": time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	return data.NewOutboxWriter(d).Write(ctx, "identity", events.AdminMFAChanged, fmt.Sprint(target), "mfa:"+randomToken(), payload)

}
func invalidateMFA(ctx context.Context, d *data.Data, u *ent.AdminUser, s *mfaState) error {
	u.AuthVersion++
	s.Challenge = ""
	s.ChallengeUntil = 0
	s.Ticket = ""
	s.TicketUntil = 0
	_, err := data.Client(ctx, d).Session.Update().Where(session.UserID(u.ID), session.RealmEQ(session.RealmAdmin), session.RevokedAtIsNil()).SetRevokedAt(time.Now().UTC()).Save(ctx)
	return err
}

// ResetAdminTwoFactor is shared by server-side recovery and the offline CLI.
// No cipher is needed: a lost data key must not prevent account recovery.
func ResetAdminTwoFactor(ctx context.Context, d *data.Data, id, actor uint64, reason string) error {
	return mfaChange(ctx, d, id, func(ctx context.Context, u *ent.AdminUser, s *mfaState) error {
		u.TotpSecret = nil
		*s = mfaState{PasswordFails: s.PasswordFails, PasswordUntil: s.PasswordUntil}
		if err := invalidateMFA(ctx, d, u, s); err != nil {
			return err
		}
		return mfaAudit(ctx, d, actor, id, "identity.totp_reset", reason)
	})
}
func (uc *IdentityUsecase) StartLogin(ctx context.Context, username, password, ip string) (*AdminLoginResult, error) {
	return uc.passwordLogin(ctx, username, password, "", ip, true)
}
func (uc *IdentityUsecase) passwordLogin(ctx context.Context, username, password, code, ip string, challenge bool) (*AdminLoginResult, error) {
	row, err := uc.data.Client.AdminUser.Query().Where(adminuser.Username(username)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrLoginFailed
	}
	if err != nil {
		return nil, err
	}
	var out *AdminLoginResult
	err = mfaChange(ctx, uc.data, row.ID, func(ctx context.Context, u *ent.AdminUser, s *mfaState) error {
		if err := checkPassword(u, s, password); err != nil {
			return err
		}
		if len(u.TotpSecret) > 0 {
			if challenge {
				if err := factorAllowed(s); err != nil {
					return err
				}
				token := fmt.Sprintf("%d.%s", u.ID, randomToken())
				s.Challenge = hashToken(token)
				s.ChallengeUntil = time.Now().Add(5 * time.Minute).Unix()
				out = &AdminLoginResult{Challenge: token}
				return nil
			}
			if code == "" {
				return ErrTOTPRequired
			}
			if _, err := uc.factor(u, s, code); err != nil {
				return err
			}
		}
		var err error
		out, err = uc.issueSession(ctx, u, ip, time.Now().Add(14*24*time.Hour))
		return err
	})
	return out, err
}
func (uc *IdentityUsecase) FinishLogin(ctx context.Context, challenge, code, ip string) (*AdminLoginResult, error) {
	var id uint64
	if _, err := fmt.Sscanf(strings.Split(challenge, ".")[0], "%d", &id); err != nil {
		return nil, ErrMFAExpired
	}
	var out *AdminLoginResult
	err := mfaChange(ctx, uc.data, id, func(ctx context.Context, u *ent.AdminUser, s *mfaState) error {
		if !u.Enabled {
			return ErrAdminDisabled
		}
		if s.Challenge == "" || s.Challenge != hashToken(challenge) || s.ChallengeUntil <= time.Now().Unix() {
			return ErrMFAExpired
		}
		recovered, err := uc.factor(u, s, code)
		if err != nil {
			return err
		}
		s.Challenge = ""
		s.ChallengeUntil = 0
		out, err = uc.issueSession(ctx, u, ip, time.Now().Add(14*24*time.Hour))
		if err != nil {
			return err
		}
		if recovered {
			ticket := randomToken()
			s.Ticket = hashToken(ticket)
			s.TicketUntil = time.Now().Add(10 * time.Minute).Unix()
			out.RecoveryTicket = ticket
		}
		return mfaAudit(ctx, uc.data, u.ID, u.ID, "identity.mfa_login", "")
	})
	if ent.IsNotFound(err) {
		err = ErrMFAExpired
	}
	return out, err
}
func (uc *IdentityUsecase) issueSession(ctx context.Context, u *ent.AdminUser, ip string, deadline time.Time) (*AdminLoginResult, error) {
	access, expires, err := uc.signer.IssueUntil(authn.RealmAdmin, u.ID, u.Username, u.RoleID, u.AuthVersion, deadline)
	if err != nil {
		return nil, err
	}
	refresh := randomToken()
	err = data.Client(ctx, uc.data).Session.Create().SetRealm(session.RealmAdmin).SetUserID(u.ID).SetAuthVersion(u.AuthVersion).SetRefreshTokenHash(hashToken(refresh)).SetIP(ip).SetExpiresAt(deadline.UTC()).Exec(ctx)
	if err != nil {
		return nil, err
	}
	if err = data.Client(ctx, uc.data).AdminUser.UpdateOneID(u.ID).SetLastLoginAt(time.Now().UTC()).SetLastLoginIP(ip).Exec(ctx); err != nil {
		return nil, err
	}
	return &AdminLoginResult{AccessToken: access, RefreshToken: refresh, ExpiresAt: expires, Admin: AdminUser{ID: u.ID, Username: u.Username, Nickname: u.Nickname, Avatar: u.Avatar, RoleID: u.RoleID, Enabled: u.Enabled, TOTPSecretEnc: u.TotpSecret}}, nil
}
func (uc *IdentityUsecase) SetupMFA(ctx context.Context, id uint64, version int, password, code, ticket string) (secret, url string, err error) {
	err = mfaChange(ctx, uc.data, id, func(ctx context.Context, u *ent.AdminUser, s *mfaState) error {
		if u.AuthVersion != version {
			return ErrSessionInvalid
		}
		if err := checkPassword(u, s, password); err != nil {
			return err
		}
		if len(u.TotpSecret) > 0 {
			if ticket != "" && s.Ticket != "" && s.Ticket == hashToken(ticket) && s.TicketUntil > time.Now().Unix() {
				s.Ticket = ""
				s.TicketUntil = 0
			} else if _, err := uc.factor(u, s, code); err != nil {
				return err
			}
		}
		var err error
		secret, url, err = authn.GenerateTOTP(u.Username)
		if err != nil {
			return err
		}
		s.Pending, err = uc.cipher.Seal([]byte(secret), []byte(fmt.Sprintf("totp:%d", u.ID)))
		if err != nil {
			return err
		}
		s.PendingUntil = time.Now().Add(10 * time.Minute).Unix()
		return mfaAudit(ctx, uc.data, id, id, "identity.totp_setup", "")
	})
	return
}
func (uc *IdentityUsecase) ConfirmMFA(ctx context.Context, id uint64, version int, code string) (codes []string, err error) {
	err = mfaChange(ctx, uc.data, id, func(ctx context.Context, u *ent.AdminUser, s *mfaState) error {
		if !u.Enabled || u.AuthVersion != version {
			return ErrSessionInvalid
		}
		if len(s.Pending) == 0 || s.PendingUntil <= time.Now().Unix() {
			return ErrMFAExpired
		}
		if err := factorAllowed(s); err != nil {
			return err
		}
		secret, err := uc.cipher.Open(s.Pending, []byte(fmt.Sprintf("totp:%d", id)))
		if err != nil {
			return err
		}
		step := matchStep(string(secret), code, time.Now())
		if step < 0 {
			return factorFail(s)
		}
		u.TotpSecret = s.Pending
		s.Pending = nil
		s.PendingUntil = 0
		s.BoundAt = time.Now().Unix()
		s.LastStep = step
		s.FactorFails = 0
		s.FactorUntil = 0
		s.Recovery = nil
		for i := 0; i < 10; i++ {
			code := strings.ToUpper(randomToken()[:20])
			codes = append(codes, code)
			s.Recovery = append(s.Recovery, hashToken(code))
		}
		if err = invalidateMFA(ctx, uc.data, u, s); err != nil {
			return err
		}
		return mfaAudit(ctx, uc.data, id, id, "identity.totp_bound", "")
	})
	return
}
func (uc *IdentityUsecase) DisableMFA(ctx context.Context, id uint64, version int, password, code string) error {
	return mfaChange(ctx, uc.data, id, func(ctx context.Context, u *ent.AdminUser, s *mfaState) error {
		if u.AuthVersion != version {
			return ErrSessionInvalid
		}
		if err := checkPassword(u, s, password); err != nil {
			return err
		}
		if _, err := uc.factor(u, s, code); err != nil {
			return err
		}
		u.TotpSecret = nil
		*s = mfaState{}
		if err := invalidateMFA(ctx, uc.data, u, s); err != nil {
			return err
		}
		return mfaAudit(ctx, uc.data, id, id, "identity.totp_disabled", "")
	})
}
func (uc *IdentityUsecase) ResetOtherMFA(ctx context.Context, actor, target uint64, version int, password, code, reason string) error {
	if strings.TrimSpace(reason) == "" || len(reason) > 255 {
		return ErrMFAConflict
	}
	return mfaChange(ctx, uc.data, actor, func(ctx context.Context, u *ent.AdminUser, s *mfaState) error {
		if actor == target || u.AuthVersion != version {
			return ErrMFAConflict
		}
		if err := checkPassword(u, s, password); err != nil {
			return err
		}
		if len(u.TotpSecret) > 0 {
			if _, err := uc.factor(u, s, code); err != nil {
				return err
			}
		}
		return ResetAdminTwoFactor(ctx, uc.data, target, actor, reason)
	})
}

func (uc *IdentityUsecase) CancelMFA(ctx context.Context, id uint64, version int) error {
	return mfaChange(ctx, uc.data, id, func(ctx context.Context, u *ent.AdminUser, s *mfaState) error {
		if u.AuthVersion != version {
			return ErrSessionInvalid
		}
		s.Pending = nil
		s.PendingUntil = 0
		return nil
	})
}

// A unique numbered slot bounds submissions across processes, including races.
func (uc *IdentityUsecase) CheckLoginIP(ctx context.Context, ip string) error {
	if ip == "" {
		ip = "unknown"
	}
	now := time.Now()
	prefix := fmt.Sprintf("admin-login:%s:%d:", hashToken(ip), now.Unix()/60)
	c := data.Client(ctx, uc.data)
	count, err := c.RiskLockKey.Query().Where(risklockkey.KeyHashHasPrefix(prefix)).Count(ctx)
	if err != nil {
		return err
	}
	if count >= 30 {
		return ErrIPLimited
	}
	err = c.RiskLockKey.Create().SetKeyHash(fmt.Sprintf("%s%d", prefix, count)).SetExpiresAt(now.Add(2 * time.Minute)).Exec(ctx)
	if ent.IsConstraintError(err) {
		return ErrIPLimited
	}
	if err == nil && count == 0 {
		_, _ = c.RiskLockKey.Delete().Where(risklockkey.KeyHashHasPrefix("admin-login:"), risklockkey.ExpiresAtLT(now)).Exec(ctx)
	}
	return err
}
