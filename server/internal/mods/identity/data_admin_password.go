package identity

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

var ErrPasswordInput = errors.New("identity.PASSWORD_INPUT")

// ChangeOwnPassword shares the revision lock with login, refresh and MFA changes.
// Password, session invalidation and audit either commit together or roll back.
func (uc *IdentityUsecase) ChangeOwnPassword(ctx context.Context, id uint64, version int, current, next, confirm string) error {
	if current == "" || utf8.RuneCountInString(next) < 6 || len(next) > 72 || next != confirm || next == current {
		return ErrPasswordInput
	}
	return mfaChange(ctx, uc.data, id, func(ctx context.Context, u *ent.AdminUser, state *mfaState) error {
		if u.AuthVersion != version {
			return ErrSessionInvalid
		}
		if err := checkPassword(u, state, current); err != nil {
			return err
		}
		hash, err := crypto.HashPassword(next)
		if err != nil {
			return err
		}
		if err = data.Client(ctx, uc.data).AdminUser.UpdateOneID(id).SetPasswordHash(hash).Exec(ctx); err != nil {
			return err
		}
		// Keep the bound authenticator/recovery codes; abandon unfinished setup.
		state.Pending = nil
		state.PendingUntil = 0
		if err = invalidateMFA(ctx, uc.data, u, state); err != nil {
			return err
		}
		return mfaAudit(ctx, uc.data, id, id, "identity.password_changed", "")
	})
}
