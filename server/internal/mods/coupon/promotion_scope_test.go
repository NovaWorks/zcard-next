package coupon

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"strings"
	"testing"
)

func TestPromotionScopeRejectsAccidentalGlobalWrites(t *testing.T) {
	repo, d := newMarketingData(t)
	svc := NewAdminCouponService(repo)
	ctx := context.Background()
	invalid := []string{`null`, `[]`, `{"products":[318]}`, `{"product_ids":[]}`, `{"product_ids":"318"}`, `{"product_ids":["318"]}`, `{"category_ids":[0]}`, `{"category_ids":[1.5]}`, `{"category_ids":[9007199254740992]}`}
	for _, raw := range invalid {
		_, err := svc.UpsertPromotion(ctx, &adminv1.UpsertPromotionRequest{Name: "must not save", ScopeJson: raw, Type: "percent", Discount: 9500, StartAt: 1, EndAt: 2})
		if err == nil {
			t.Fatalf("accepted dangerous scope %s", raw)
		}
	}
	if n := d.Client.Promotion.Query().CountX(ctx); n != 0 {
		t.Fatalf("invalid promotions saved: %d", n)
	}
	for _, raw := range []string{`{}`, `{"product_ids":[318,297]}`, `{"category_ids":[3,5]}`, `{"product_ids":[318],"category_ids":[3]}`} {
		got, err := svc.UpsertPromotion(ctx, &adminv1.UpsertPromotionRequest{Name: "valid", ScopeJson: raw, Type: "percent", Discount: 9500, StartAt: 1, EndAt: 2})
		if err != nil || got == nil {
			t.Fatalf("%s: %v", raw, err)
		}
		row := d.Client.Promotion.GetX(ctx, got.Id)
		if strings.Contains(raw, "318") && !promoScopeHit(row.Scope, 318, 0) {
			t.Fatal("product not matched")
		}
		if raw != "{}" && promoScopeHit(row.Scope, 999, 999) {
			t.Fatal("unrelated product matched")
		}
	}
}
