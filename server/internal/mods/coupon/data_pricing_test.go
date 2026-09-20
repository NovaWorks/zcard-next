package coupon

import (
	"context"
	"testing"
	"time"

	couponent "github.com/NovaWorks/zcard-next/server/internal/data/ent/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/promotion"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

func TestCouponScopedAmount(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scope map[string]any
		level uint64
		want  money.Cents
	}{
		{"category only", map[string]any{"category_ids": []any{float64(10)}}, 0, 300},
		{"product and category OR without double counting", map[string]any{"product_ids": []any{"1"}, "category_ids": []any{float64(10)}}, 0, 300},
		{"union of product and category", map[string]any{"product_ids": []any{"2"}, "category_ids": []any{float64(10)}}, 0, 1300},
		{"matched level covers cart", map[string]any{"product_ids": []any{"1"}, "level_ids": []any{float64(9)}}, 9, 1300},
		{"unmatched level still allows matched product", map[string]any{"product_ids": []any{"1"}, "level_ids": []any{float64(9)}}, 8, 300},
		{"empty scope covers cart", nil, 0, 1300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, d := newMarketingData(t)
			ctx := context.Background()
			d.Client.Coupon.Create().SetName("90% payable").SetCode("TEST").SetType(couponent.TypePercent).SetValue(9000).SetScope(tc.scope).SaveX(ctx)
			items := []port.CartItem{{ProductID: 1, CategoryID: 10, Quantity: 3, UnitPrice: 1000}, {ProductID: 2, CategoryID: 20, Quantity: 1, UnitPrice: 10000}}
			v, _, err := r.ResolveScoped(ctx, "TEST", 7, tc.level, items)
			if err != nil || v != tc.want {
				t.Fatalf("discount=%d err=%v want=%d", v, err, tc.want)
			}
		})
	}
}

func TestCouponRatesAgreeAcrossResolvers(t *testing.T) {
	for _, tc := range []struct {
		rate, base int64
		want       money.Cents
		invalid    bool
	}{
		{9800, 3600, 72, false}, {9500, 199, 9, false}, {9800, 1, 0, false},
		{1000, 3600, 3240, false}, {10000, 3600, 0, false},
		{0, 3600, 0, true}, {-1, 3600, 0, true}, {10001, 3600, 0, true},
	} {
		r, d := newMarketingData(t)
		ctx := context.Background()
		d.Client.Coupon.Create().SetName("rate").SetCode("TEST").SetType(couponent.TypePercent).SetValue(tc.rate).SaveX(ctx)
		legacy, _, err := r.Resolve(ctx, "TEST", 7, money.Cents(tc.base))
		if (err != nil) != tc.invalid || legacy != tc.want {
			t.Fatalf("legacy rate=%d discount=%d err=%v", tc.rate, legacy, err)
		}
		scoped, _, err := r.ResolveScoped(ctx, "TEST", 7, 0, []port.CartItem{{ProductID: 1, Quantity: 1, UnitPrice: money.Cents(tc.base)}})
		if (err != nil) != tc.invalid || scoped != tc.want {
			t.Fatalf("scoped rate=%d discount=%d err=%v", tc.rate, scoped, err)
		}
		if tc.invalid {
			if n, err := r.CreateBatch(ctx, "invalid", "percent", tc.rate, 1, nil); err == nil || n != 0 {
				t.Fatalf("invalid coupon rate created: rate=%d n=%d err=%v", tc.rate, n, err)
			}
		}
	}
}

func TestPromotionBestForPayableRates(t *testing.T) {
	r, d := newMarketingData(t)
	ctx := context.Background()
	create := func(name string, typ promotion.Type, discount int64) {
		d.Client.Promotion.Create().SetName(name).SetType(typ).SetDiscount(discount).SetScope(map[string]any{}).
			SetStartAt(time.Now().UTC().Add(-time.Hour)).SetEndAt(time.Now().UTC().Add(time.Hour)).SetEnabled(true).SaveX(ctx)
	}
	create("90% payable", promotion.TypePercent, 9000)
	create("small fixed", promotion.TypeFixed, 500)
	create("invalid rate", promotion.TypePercent, 10001)
	create("overflow rate", promotion.TypePercent, 1<<32+9000)
	create("oversized fixed", promotion.TypeFixed, 10000)
	create("zero special price", promotion.TypeSpecialPrice, 0)
	best, err := r.BestFor(ctx, 1, 0, 10000)
	if err != nil || best == nil || best.Name != "90% payable" || best.DiscountFor(10000) != 1000 {
		t.Fatalf("incorrect best promotion: %+v err=%v", best, err)
	}
	// A competing fixed promotion can win at a smaller discounted price.
	best, err = r.BestFor(ctx, 1, 0, 4000)
	if err != nil || best == nil || best.Name != "small fixed" || best.DiscountFor(4000) != 500 {
		t.Fatalf("incorrect discounted-price best promotion: %+v err=%v", best, err)
	}
	for _, rate := range []int64{-1, 0, 10001, 1<<32 + 9000} {
		if _, err := r.UpsertPromotion(ctx, 0, "invalid", map[string]any{}, "percent", 0, rate, 0, time.Now(), time.Now().Add(time.Hour), true); err == nil {
			t.Fatalf("invalid promotion rate accepted: %d", rate)
		}
	}
}
