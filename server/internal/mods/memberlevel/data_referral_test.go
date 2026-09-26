package memberlevel

import (
	"context"
	"fmt"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
)

func TestReferralRegistrationAndAdminOverride(t *testing.T) {
	d, repo, _, _ := newMemberLevelEnv(t)
	ctx := context.Background()
	actor := identity.WithClaims(ctx, &authn.Claims{Subject: 99})
	admin := NewAdminMemberLevelService(repo)
	users := identity.NewUserRepo(d)
	gift := d.Client.MemberLevel.Create().SetName("推荐会员").SetDiscount(9000).SetThresholdConsume(10000).SetThresholdType("consume").SetEnabled(true).SaveX(ctx)
	better := d.Client.MemberLevel.Create().SetName("高级会员").SetDiscount(8500).SetSort(2).SetThresholdConsume(20000).SetThresholdType("consume").SetEnabled(true).SaveX(ctx)
	inviter := d.Client.User.Create().SetUsername("agent").SetPromoCode("ABCDEFGH").SaveX(ctx)
	if _, err := admin.ConfigureInviteLevel(actor, &adminv1.ConfigureInviteLevelRequest{UserId: inviter.ID, LevelId: gift.ID, Reason: "邀请新人"}); err != nil {
		t.Fatal(err)
	}
	for i, code := range []string{"abcdefgh", fmt.Sprint(inviter.ID)} {
		u, err := users.Register(ctx, identity.RegisterInput{Username: fmt.Sprintf("customer%d", i), Password: "test-pass", InviteCode: code})
		if err != nil {
			t.Fatal(err)
		}
		if u.ReferralLevelID != gift.ID || u.ManualLevelID != 0 || u.InviteLevelID != 0 || u.InviteL1 != inviter.ID {
			t.Fatalf("bad registration: %+v", u)
		}
		rate, id, source, err := repo.EffectiveState(ctx, u.ID)
		if err != nil || rate != 9000 || id != gift.ID || source != "referral" {
			t.Fatal(rate, id, source, err)
		}
		p, err := repo.ResolveProgress(ctx, u.ID)
		if err != nil || p.Next == nil || p.Next.ID != better.ID {
			t.Fatal(p, err)
		}
		// Changing the inviter must not rewrite an existing customer's entitlement.
		if _, err := admin.ConfigureInviteLevel(actor, &adminv1.ConfigureInviteLevelRequest{UserId: inviter.ID, LevelId: better.ID, Reason: "下批客户"}); err != nil {
			t.Fatal(err)
		}
		if rate, _, _ := repo.EffectiveRate(ctx, u.ID); rate != 9000 {
			t.Fatal("retroactive change")
		}
		// Administrator may select an automatic level without fabricating spending.
		if _, err := admin.AssignUserLevel(actor, &adminv1.AssignUserLevelRequest{UserId: u.ID, LevelId: better.ID, Reason: "人工调整"}); err != nil {
			t.Fatal(err)
		}
		if rate, _, source, err := repo.EffectiveState(ctx, u.ID); err != nil || rate != 8500 || source != "manual" {
			t.Fatal(rate, source, err)
		}
		if _, err := repo.UpdateLevel(ctx, better.ID, better.Name, 8500, 2, true, nil, LevelSettings{AcquireMode: "auto"}); err != nil {
			t.Fatal("editing an assigned automatic level", err)
		}
		if _, err := admin.AssignUserLevel(actor, &adminv1.AssignUserLevelRequest{UserId: u.ID, Reason: "取消指定"}); err != nil {
			t.Fatal(err)
		}
		if rate, _, source, _ := repo.EffectiveState(ctx, u.ID); rate != 9000 || source != "referral" {
			t.Fatal(rate, source)
		}
		if _, err := admin.AssignUserLevel(actor, &adminv1.AssignUserLevelRequest{UserId: u.ID, ClearReferral: true, Reason: "撤销资格"}); err != nil {
			t.Fatal(err)
		}
		if rate, _, _, _ := repo.EffectiveState(ctx, u.ID); rate != 0 {
			t.Fatal("revoked referral still applies")
		}
		if d.Client.User.GetX(ctx, u.ID).InviteL1 != inviter.ID {
			t.Fatal("revocation changed attribution")
		}
		if _, err := admin.ConfigureInviteLevel(actor, &adminv1.ConfigureInviteLevelRequest{UserId: inviter.ID, LevelId: gift.ID, Reason: "恢复设置"}); err != nil {
			t.Fatal(err)
		}
	}
	// Disable affects future registrations only; the invite relation still binds.
	if _, err := admin.ConfigureInviteLevel(actor, &adminv1.ConfigureInviteLevelRequest{UserId: inviter.ID, Reason: "停止赠送"}); err != nil {
		t.Fatal(err)
	}
	u, err := users.Register(ctx, identity.RegisterInput{Username: "no-benefit", Password: "test-pass", InviteCode: inviter.PromoCode})
	if err != nil || u.ReferralLevelID != 0 || u.InviteL1 != inviter.ID {
		t.Fatal(u, err)
	}
}

func TestReferralAutomaticProgressAndPrivacy(t *testing.T) {
	d, repo, _, _ := newMemberLevelEnv(t)
	ctx := context.Background()
	gift := d.Client.MemberLevel.Create().SetName("私有推荐").SetAcquireMode("manual").SetDisplayMode("hidden").SetDiscount(9000).SetEnabled(true).SaveX(ctx)
	auto := d.Client.MemberLevel.Create().SetName("消费会员").SetDiscount(8500).SetThresholdType("consume").SetThresholdConsume(10000).SetEnabled(true).SaveX(ctx)
	agent := d.Client.User.Create().SetUsername("agent").SetPromoCode("ABCDEFGH").SetInviteLevelID(gift.ID).SaveX(ctx)
	u, err := identity.NewUserRepo(d).Register(ctx, identity.RegisterInput{Username: "invitee", Password: "test-pass", InviteCode: agent.PromoCode})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewStoreMemberLevelService(repo, nil)
	preview, err := svc.GetInviteBenefit(ctx, &storefrontv1.InviteBenefitRequest{Code: agent.PromoCode})
	if err != nil || !preview.Valid || !preview.HasBenefit || preview.Level != nil {
		t.Fatal("private preview leaks", preview, err)
	}
	reply, err := svc.GetMyLevel(identity.WithClaims(ctx, &authn.Claims{Subject: u.ID}), nil)
	if err != nil || !reply.PrivateLevel || reply.Current != nil || reply.Next != nil || reply.Source != "referral" {
		t.Fatal(reply, err)
	}
	d.Client.Order.Create().SetOrderNo("paid").SetUserID(u.ID).SetStatus(order.StatusPaid).SetTotalAmount(10000).SaveX(ctx)
	if rate, id, source, err := repo.EffectiveState(ctx, u.ID); err != nil || rate != 8500 || id != auto.ID || source != "auto" {
		t.Fatal(rate, id, source, err)
	}
	// Equal rates prefer auto; 0 is full price, not a free order.
	d.Client.MemberLevel.UpdateOneID(auto.ID).SetDiscount(9000).ExecX(ctx)
	if _, _, source, _ := repo.EffectiveState(ctx, u.ID); source != "auto" {
		t.Fatal(source)
	}
	d.Client.MemberLevel.UpdateOneID(auto.ID).SetDiscount(0).ExecX(ctx)
	if rate, _, source, _ := repo.EffectiveState(ctx, u.ID); rate != 9000 || source != "referral" {
		t.Fatal(rate, source)
	}
	// Refund recalculates the automatic tier, but preserves the registration grant.
	d.Client.Order.Update().SetStatus(order.StatusRefunded).ExecX(ctx)
	if rate, _, source, _ := repo.EffectiveState(ctx, u.ID); rate != 9000 || source != "referral" {
		t.Fatal(rate, source)
	}
}

func TestReferralGuardsAndRollback(t *testing.T) {
	d, repo, _, _ := newMemberLevelEnv(t)
	ctx := context.Background()
	actor := identity.WithClaims(ctx, &authn.Claims{Subject: 99})
	admin := NewAdminMemberLevelService(repo)
	gift := d.Client.MemberLevel.Create().SetName("gift").SetEnabled(true).SaveX(ctx)
	agent := d.Client.User.Create().SetUsername("agent").SetPromoCode("ABCDEFGH").SetInviteLevelID(gift.ID).SaveX(ctx)
	if err := repo.DeleteLevel(ctx, gift.ID); err == nil {
		t.Fatal("deleted configured level")
	}
	if _, err := repo.UpdateLevel(ctx, gift.ID, "gift", 9000, 0, false, nil); err == nil {
		t.Fatal("disabled configured level")
	}
	users := identity.NewUserRepo(d)
	u, err := users.Register(ctx, identity.RegisterInput{Username: "customer", Password: "test-pass", InviteCode: agent.PromoCode})
	if err != nil {
		t.Fatal(err)
	}
	d.Client.User.UpdateOneID(agent.ID).SetInviteLevelID(0).ExecX(ctx)
	if err := repo.DeleteLevel(ctx, gift.ID); err == nil {
		t.Fatal("deleted granted level")
	}
	count, audits := d.Client.User.Query().CountX(ctx), d.Client.AuditLog.Query().CountX(ctx)
	if _, err := users.Register(ctx, identity.RegisterInput{Username: "customer", Password: "test-pass", InviteCode: agent.PromoCode}); err == nil {
		t.Fatal("duplicate succeeded")
	}
	if d.Client.User.Query().CountX(ctx) != count || d.Client.AuditLog.Query().CountX(ctx) != audits {
		t.Fatal("partial duplicate writes")
	}
	for _, bad := range []string{"missing", "184467440737095516160000"} {
		if _, err := users.Register(ctx, identity.RegisterInput{Username: "invalid", Password: "test-pass", InviteCode: bad}); err == nil {
			t.Fatal("invalid code accepted")
		}
	}
	d.Client.User.UpdateOneID(agent.ID).SetInviteLevelID(99999).ExecX(ctx)
	if _, err := users.Register(ctx, identity.RegisterInput{Username: "invalid", Password: "test-pass", InviteCode: agent.PromoCode}); err == nil {
		t.Fatal("invalid level accepted")
	}
	d.Client.User.UpdateOneID(agent.ID).SetInviteLevelID(gift.ID).SetStatus("banned").ExecX(ctx)
	if _, err := users.Register(ctx, identity.RegisterInput{Username: "invalid", Password: "test-pass", InviteCode: agent.PromoCode}); err == nil {
		t.Fatal("banned inviter accepted")
	}
	if d.Client.User.Query().CountX(ctx) != count {
		t.Fatal("failed register left account")
	}
	if _, err := admin.AssignUserLevel(ctx, &adminv1.AssignUserLevelRequest{UserId: u.ID, LevelId: gift.ID, Reason: "unauth"}); err == nil {
		t.Fatal("unauth assignment")
	}
	if _, err := admin.ConfigureInviteLevel(ctx, &adminv1.ConfigureInviteLevelRequest{UserId: u.ID, LevelId: gift.ID, Reason: "unauth"}); err == nil {
		t.Fatal("unauth config")
	}
	if _, err := admin.AssignUserLevel(actor, &adminv1.AssignUserLevelRequest{UserId: u.ID, LevelId: gift.ID, ClearReferral: true, Reason: "ambiguous"}); err == nil {
		t.Fatal("ambiguous command")
	}
}
