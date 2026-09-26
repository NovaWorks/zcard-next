package order

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	orderent "github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"testing"
)

func TestInvitedMemberOrderAndAdminChange(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	d.Client.Product.UpdateOneID(1).SetPrice(10000).ExecX(ctx)
	gift := d.Client.MemberLevel.Create().SetName("gift").SetAcquireMode("manual").SetDiscount(9000).SetEnabled(true).SaveX(ctx)
	lower := d.Client.MemberLevel.Create().SetName("lower discount").SetAcquireMode("manual").SetDiscount(9500).SetEnabled(true).SaveX(ctx)
	inviter := d.Client.User.Create().SetUsername("agent").SetPromoCode("ABCDEFGH").SetInviteLevelID(gift.ID).SaveX(ctx)
	buyer, err := identity.NewUserRepo(d).Register(ctx, identity.RegisterInput{Username: "buyer", Password: "test-pass", InviteCode: inviter.PromoCode})
	if err != nil {
		t.Fatal(err)
	}
	levels := memberlevel.NewMemberLevelRepoImpl(d, nil)
	uc.MemberRate = levels
	uc.Coupon = coupon.NewCouponRepoImpl(d)
	d.Client.Coupon.Create().SetName("level coupon").SetCode("GIFT").SetType("fixed").SetValue(100).SetScope(map[string]any{"level_ids": []any{float64(gift.ID)}}).SaveX(ctx)
	first, err := uc.CreateOrder(ctx, CreateOrderInput{UserID: buyer.ID, QueryPassword: "test-pass", CouponCode: "GIFT", Items: []OrderItemInput{{ProductID: 1, Quantity: 1}}})
	if err != nil || first.TotalCents != 8900 {
		t.Fatal(first, err)
	}
	admin := memberlevel.NewAdminMemberLevelService(levels)
	actor := identity.WithClaims(ctx, &authn.Claims{Subject: 99})
	if _, err := admin.AssignUserLevel(actor, &adminv1.AssignUserLevelRequest{UserId: buyer.ID, LevelId: lower.ID, Reason: "后台修改优先"}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ConfigureInviteLevel(actor, &adminv1.ConfigureInviteLevelRequest{UserId: inviter.ID, Reason: "关闭赠送"}); err != nil {
		t.Fatal(err)
	}
	second, err := uc.CreateOrder(ctx, CreateOrderInput{UserID: buyer.ID, QueryPassword: "test-pass", Items: []OrderItemInput{{ProductID: 1, Quantity: 1}}})
	if err != nil || second.TotalCents != 9500 {
		t.Fatal(second, err)
	}
	old := d.Client.Order.Query().Where(orderent.OrderNo(first.OrderNo)).OnlyX(ctx)
	if old.TotalAmount != 8900 || old.InviteL1 != inviter.ID {
		t.Fatal("old order or attribution changed")
	}
	if d.Client.User.GetX(ctx, buyer.ID).ReferralLevelID != gift.ID {
		t.Fatal("inviter change overwrote customer")
	}
}
