//go:build integration

package testint

import (
	"context"
	"fmt"
	"sync"
	"testing"

	kerrors "github.com/go-kratos/kratos/v3/errors"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
)

func TestReferralLevelsMySQL(t *testing.T) { runReferralLevels(MySQL(t)) }
func TestReferralLevelsPG(t *testing.T)    { runReferralLevels(PG(t)) }
func runReferralLevels(h *Harness) {
	t, ctx, c := h.T, context.Background(), h.Data.Client
	repo := memberlevel.NewMemberLevelRepoImpl(h.Data, nil)
	users := identity.NewUserRepo(h.Data)
	admin := memberlevel.NewAdminMemberLevelService(repo)
	actor := identity.WithClaims(ctx, &authn.Claims{Subject: 1})
	level := c.MemberLevel.Create().SetName("gift").SetAcquireMode("manual").SetDiscount(9000).SetEnabled(true).SaveX(ctx)
	other := c.MemberLevel.Create().SetName("override").SetAcquireMode("manual").SetDiscount(8500).SetEnabled(true).SaveX(ctx)
	inviter := c.User.Create().SetUsername("agent").SetPromoCode("ABCDEFGH").SetInviteLevelID(level.ID).SaveX(ctx)
	u, err := users.Register(ctx, identity.RegisterInput{Username: "customer", Password: "test-pass", InviteCode: inviter.PromoCode})
	if err != nil {
		t.Fatal(err)
	}
	if rate, id, err := repo.EffectiveRate(ctx, u.ID); err != nil || rate != 9000 || id != level.ID {
		t.Fatal(rate, id, err)
	}
	if _, err := admin.AssignUserLevel(actor, &adminv1.AssignUserLevelRequest{UserId: u.ID, LevelId: other.ID, Reason: "客户调级"}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ConfigureInviteLevel(actor, &adminv1.ConfigureInviteLevelRequest{UserId: inviter.ID, LevelId: other.ID, Reason: "新客户配置"}); err != nil {
		t.Fatal(err)
	}
	if rate, id, err := repo.EffectiveRate(ctx, u.ID); err != nil || rate != 8500 || id != other.ID {
		t.Fatal(rate, id, err)
	}
	// Duplicate registration must roll back its audit/grant as well (including PG aborted tx semantics).
	before := c.AuditLog.Query().CountX(ctx)
	if _, err := users.Register(ctx, identity.RegisterInput{Username: "customer", Password: "test-pass", InviteCode: inviter.PromoCode}); err == nil {
		t.Fatal("duplicate succeeded")
	}
	if c.AuditLog.Query().CountX(ctx) != before {
		t.Fatal("duplicate audit")
	}
	// Concurrent registrations and configuration changes may reject a stale configuration,
	// but every successful registration must retain exactly one valid grant and attribution.
	var wg sync.WaitGroup
	type registrationResult struct {
		username string
		err      error
	}
	results := make(chan registrationResult, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			username := fmt.Sprintf("concurrent%d", i)
			_, err := users.Register(ctx, identity.RegisterInput{Username: username, Password: "test-pass", InviteCode: inviter.PromoCode})
			results <- registrationResult{username: username, err: err}
		}(i)
	}
	for i := 0; i < 4; i++ {
		target := level.ID
		if i%2 == 0 {
			target = other.ID
		}
		if _, err := admin.ConfigureInviteLevel(actor, &adminv1.ConfigureInviteLevelRequest{UserId: inviter.ID, LevelId: target, Reason: "并发更新"}); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	close(results)
	successes := 0
	var rejected []string
	for result := range results {
		if result.err == nil {
			successes++
			continue
		}
		if kerrors.FromError(result.err).Reason != "identity.INVITE_CHANGED" {
			t.Fatal("unexpected registration failure", result.err)
		}
		rejected = append(rejected, result.username)
	}
	if c.User.Query().CountX(ctx) != 2+successes {
		t.Fatal("failed registration left a user")
	}
	if c.AuditLog.Query().CountX(ctx) != before+4+successes {
		t.Fatal("registration and audit not atomic")
	}
	// Every racing request may legitimately reject a changed configuration.
	// Once configuration is stable, a new registration and retries with the same
	// rejected usernames must succeed and grant the final configured level.
	for _, username := range append(rejected, "stablecustomer") {
		registered, err := users.Register(ctx, identity.RegisterInput{Username: username, Password: "test-pass", InviteCode: inviter.PromoCode})
		if err != nil {
			t.Fatalf("stable registration %s: %v", username, err)
		}
		if registered.ReferralLevelID != level.ID || registered.InviteL1 != inviter.ID {
			t.Fatalf("stable registration used stale grant or attribution: %s", username)
		}
	}
	if c.User.Query().CountX(ctx) != 11 || c.AuditLog.Query().CountX(ctx) != before+4+9 {
		t.Fatal("stable registrations and retries must each create one user and one audit")
	}
	rows := c.User.Query().AllX(ctx)
	for _, row := range rows {
		if row.ID == inviter.ID {
			continue
		}
		if row.ReferralLevelID != level.ID && row.ReferralLevelID != other.ID {
			t.Fatal("partial grant", row.ID)
		}
		if row.InviteL1 != inviter.ID {
			t.Fatal("partial attribution")
		}
	}
	if err := repo.DeleteLevel(ctx, level.ID); err == nil {
		t.Fatal("deleted customer grant")
	}
}
