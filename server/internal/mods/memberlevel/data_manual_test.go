package memberlevel

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"testing"
)

func TestPrivateLevelAssignmentAndPublicSanitization(t *testing.T) {
	d, r, _, _ := newMemberLevelEnv(t)
	ctx := context.Background()
	u := d.Client.User.Create().SetUsername("agent").SaveX(ctx)
	lv, err := r.CreateLevel(ctx, "代理", "recharge", 0, 0, 7300, 99, true, map[string]any{"points": 99}, LevelSettings{AcquireMode: "manual", DisplayMode: "contact"})
	if err != nil {
		t.Fatal(err)
	}
	if rate, _, err := r.EffectiveRate(ctx, u.ID); err != nil || rate != 0 {
		t.Fatal("manual level automatically granted", rate, err)
	}
	admin := NewAdminMemberLevelService(r)
	actor := identity.WithClaims(ctx, &authn.Claims{Subject: 9})
	if _, err = admin.AssignUserLevel(actor, &adminv1.AssignUserLevelRequest{UserId: u.ID, LevelId: lv.ID, Reason: "审核通过"}); err != nil {
		t.Fatal(err)
	}
	if rate, id, err := r.EffectiveRate(ctx, u.ID); err != nil || rate != 7300 || id != lv.ID {
		t.Fatal("assigned price not applied", rate, id, err)
	}
	store := NewStoreMemberLevelService(r, nil)
	got, err := store.GetMyLevel(identity.WithClaims(ctx, &authn.Claims{Subject: u.ID}), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, brief := range append(got.Levels, got.Current) {
		if brief == nil || brief.Discount != 0 || brief.Id != 0 || brief.PointsRuleJson != "" || brief.ThresholdRecharge != 0 || brief.DisplayText != "联系客服" {
			t.Fatalf("private metadata leaked: %+v", brief)
		}
	}
	if got.Next != nil || got.Progress != nil {
		t.Fatal("manual level has automatic upgrade progress")
	}
	if err = r.DeleteLevel(ctx, lv.ID); err == nil {
		t.Fatal("deleted assigned level")
	}
	if _, err = r.UpdateLevel(ctx, lv.ID, "代理", 7300, 99, false, nil); err == nil {
		t.Fatal("disabled assigned level")
	}
	if d.Client.AuditLog.Query().CountX(ctx) != 1 {
		t.Fatal("assignment audit missing")
	}
	if _, err = admin.AssignUserLevel(actor, &adminv1.AssignUserLevelRequest{UserId: u.ID, Reason: "恢复自动"}); err != nil {
		t.Fatal(err)
	}
	if err = r.DeleteLevel(ctx, lv.ID); err != nil {
		t.Fatal(err)
	}
}
