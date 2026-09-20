package order

import (
	"context"
	"strings"
	"testing"
	"time"

	couponent "github.com/NovaWorks/zcard-next/server/internal/data/ent/coupon"
	orderent "github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderamountline"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/promotion"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// Exercise actual repositories and persisted checkout/payment snapshots, rather
// than supplying precomputed discounts to the calculator.
func TestCreateOrderDiscountCombinations(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		base, other              int64
		quantity                 int32
		member, group            int32
		stackMember, stackCoupon bool
		couponType               couponent.Type
		couponValue              int64
		couponProductOnly        bool
		promoType                promotion.Type
		promoValue, threshold    int64
		flashPrice               int64
		want                     int64
		wantError                string
	}{
		{name: "scoped percentage coupon", base: 1000, other: 10000, couponType: couponent.TypePercent, couponValue: 5000, couponProductOnly: true, want: 10500},
		{name: "scoped fixed coupon capped to matching item", base: 1000, other: 10000, couponType: couponent.TypeFixed, couponValue: 5000, couponProductOnly: true, want: 10000},
		{name: "scoped coupon includes matching quantity", base: 1000, quantity: 3, other: 10000, couponType: couponent.TypePercent, couponValue: 5000, couponProductOnly: true, want: 11500},
		{name: "group excludes member", base: 10000, member: 9000, group: 8000, want: 8000},
		{name: "better member excludes group", base: 10000, member: 7000, group: 8000, want: 7000},
		{name: "group allows member", base: 10000, member: 9000, group: 8000, stackMember: true, want: 7200},
		{name: "group excludes coupon", base: 10000, group: 8000, couponType: couponent.TypeFixed, couponValue: 1000, wantError: "COUPON_INVALID"},
		{name: "group allows coupon", base: 10000, group: 8000, stackCoupon: true, couponType: couponent.TypeFixed, couponValue: 1000, want: 7000},
		{name: "unused group does not exclude coupon", base: 10000, member: 7000, group: 8000, couponType: couponent.TypeFixed, couponValue: 1000, want: 6000},
		{name: "equal member and group uses member", base: 10000, member: 8000, group: 8000, couponType: couponent.TypeFixed, couponValue: 1000, want: 7000},
		{name: "coupon only discounts unblocked cart item", base: 10000, other: 1000, group: 8000, couponType: couponent.TypeFixed, couponValue: 5000, want: 8000},
		{name: "blocked matching item cannot unlock coupon", base: 10000, other: 1000, group: 8000, couponType: couponent.TypeFixed, couponValue: 5000, couponProductOnly: true, wantError: "COUPON_INVALID"},
		{name: "percentage coupon after member", base: 10000, member: 5000, couponType: couponent.TypePercent, couponValue: 5000, want: 2500},
		{name: "98 percent coupon means payable", base: 3600, couponType: couponent.TypePercent, couponValue: 9800, want: 3528},
		{name: "100 percent coupon is neutral", base: 3600, couponType: couponent.TypePercent, couponValue: 10000, want: 3600},
		{name: "one cent coupon rounding", base: 1, couponType: couponent.TypePercent, couponValue: 9800, want: 1},
		{name: "special price after member", base: 10000, member: 9000, promoType: promotion.TypeSpecialPrice, promoValue: 5000, want: 5000},
		{name: "special price cannot raise member price", base: 10000, member: 4000, promoType: promotion.TypeSpecialPrice, promoValue: 5000, want: 4000},
		{name: "percentage promotion participates", base: 10000, promoType: promotion.TypePercent, promoValue: 9000, want: 9000},
		{name: "98 percent promotion means payable", base: 3600, promoType: promotion.TypePercent, promoValue: 9800, want: 3528},
		{name: "percentage promotion after member", base: 10000, member: 9000, promoType: promotion.TypePercent, promoValue: 9000, want: 8100},
		{name: "100 percent promotion is neutral", base: 10000, promoType: promotion.TypePercent, promoValue: 10000, want: 10000},
		{name: "flash excludes promotion", base: 10000, flashPrice: 5000, promoType: promotion.TypePercent, promoValue: 9000, want: 5000},
		{name: "flash excludes coupon by default", base: 10000, flashPrice: 5000, couponType: couponent.TypePercent, couponValue: 9000, want: 5000},
		{name: "promotion threshold uses discounted price", base: 10000, member: 9000, promoType: promotion.TypeFixed, promoValue: 1000, threshold: 9500, want: 9000},
		{name: "coupon after special price", base: 10000, member: 9000, promoType: promotion.TypeSpecialPrice, promoValue: 5000, couponType: couponent.TypePercent, couponValue: 9000, want: 4500},
		{name: "quantity amount lines", base: 10000, quantity: 3, member: 9800, want: 29400},
		{name: "multiple items quantities and coupon", base: 10000, quantity: 3, other: 2000, member: 9800, couponType: couponent.TypeFixed, couponValue: 1000, want: 30360},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, uc, payRepo := newIdemEnv(t)
			ctx := context.Background()
			d.Client.Product.UpdateOneID(1).SetPrice(tc.base).ExecX(ctx)
			uc.Catalog = catalog.NewProductRepoImpl(d, nil)
			cp := coupon.NewCouponRepoImpl(d)
			uc.Coupon, uc.Promos = cp, cp
			qty := tc.quantity
			if qty == 0 {
				qty = 1
			}
			in := CreateOrderInput{UserID: 3, Contact: "test@example.invalid", QueryPassword: "test-only", Items: []OrderItemInput{{ProductID: 1, Quantity: qty}}}
			if tc.other > 0 {
				p := d.Client.Product.Create().SetName("other").SetSlug("other").SetPrice(tc.other).SetStockType("card").SetStatus(1).SaveX(ctx)
				in.Items = append(in.Items, OrderItemInput{ProductID: p.ID, Quantity: 1})
			}
			if tc.member > 0 {
				levels := memberlevel.NewMemberLevelRepoImpl(d, nil)
				if _, err := levels.CreateLevel(ctx, "member", "consume", 0, 0, tc.member, 1, true, nil); err != nil {
					t.Fatal(err)
				}
				uc.MemberRate = levels
			}
			if tc.group > 0 {
				d.Client.MemberProductGroup.Create().SetName("group").SetProductIds([]uint64{1}).SetDiscount(tc.group).
					SetStackMember(tc.stackMember).SetStackCoupon(tc.stackCoupon).SaveX(ctx)
			}
			if tc.couponType != "" {
				scope := map[string]any{}
				if tc.couponProductOnly {
					scope["product_ids"] = []any{float64(1)}
				}
				d.Client.Coupon.Create().SetName("coupon").SetCode("TEST").SetType(tc.couponType).SetValue(tc.couponValue).SetScope(scope).SaveX(ctx)
				in.CouponCode = "TEST"
			}
			if tc.promoType != "" {
				d.Client.Promotion.Create().SetName("promotion").SetScope(map[string]any{}).SetType(tc.promoType).
					SetDiscount(tc.promoValue).SetSpecialPrice(tc.promoValue).SetThreshold(tc.threshold).
					SetStartAt(time.Now().UTC().Add(-time.Hour)).SetEndAt(time.Now().UTC().Add(time.Hour)).SetEnabled(true).SaveX(ctx)
			}
			if tc.flashPrice > 0 {
				if _, err := cp.CreateFlash(ctx, 1, 0, tc.flashPrice, time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour), 10, 0); err != nil {
					t.Fatal(err)
				}
				uc.Flash = cp
			}
			res, err := uc.CreateOrder(ctx, in)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error=%v, want %s", err, tc.wantError)
				}
				if d.Client.Order.Query().CountX(ctx) != 0 || d.Client.OrderItem.Query().CountX(ctx) != 0 || d.Client.OrderAmountLine.Query().CountX(ctx) != 0 {
					t.Fatal("rejected coupon left order records")
				}
				if d.Client.Coupon.Query().OnlyX(ctx).Status != couponent.StatusUnused {
					t.Fatal("rejected coupon was consumed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			o := d.Client.Order.Query().Where(orderent.OrderNo(res.OrderNo)).OnlyX(ctx)
			if res.TotalCents != tc.want || o.TotalAmount != tc.want {
				t.Fatalf("reply=%d persisted=%d want=%d", res.TotalCents, o.TotalAmount, tc.want)
			}
			items := d.Client.OrderItem.Query().Where(orderitem.OrderID(o.ID)).AllX(ctx)
			itemTotals := map[uint64]int64{}
			for _, item := range items {
				itemTotals[item.ID] = item.Amount
				if item.ProductID == 1 && item.UnitPrice != tc.base {
					t.Fatalf("unit price snapshot changed to %d", item.UnitPrice)
				}
			}
			lines := d.Client.OrderAmountLine.Query().Where(orderamountline.OrderID(o.ID)).Order(orderamountline.BySeq()).AllX(ctx)
			var sum int64
			for i, line := range lines {
				sum += line.Amount
				if line.Seq != int32(i) {
					t.Fatalf("non-contiguous order amount sequence: %d at %d", line.Seq, i)
				}
				if line.Type != orderamountline.TypeCouponDiscount {
					if _, ok := itemTotals[line.ItemID]; !ok {
						t.Fatalf("line has no matching order item: %+v", line)
					}
					itemTotals[line.ItemID] -= line.Amount
				}
			}
			if sum != tc.want {
				t.Fatalf("amount lines sum=%d want=%d", sum, tc.want)
			}
			if tc.flashPrice > 0 && tc.couponType != "" && d.Client.Coupon.Query().OnlyX(ctx).Status != couponent.StatusUnused {
				t.Fatal("flash-exclusive coupon was consumed")
			}
			for itemID, remaining := range itemTotals {
				if remaining != 0 {
					t.Fatalf("item %d has unreconciled amount %d", itemID, remaining)
				}
			}
			d.Client.PaymentChannel.Create().SetName("test epay").SetCode("test-epay").SetDriver("epay").SetConfig([]byte("{}")).SetEnabled(true).SaveX(ctx)
			p, err := payRepo.CreatePayment(ctx, o.ID, "test-epay", o.TotalAmount, "")
			if err != nil || p.Amount != tc.want {
				t.Fatalf("payment=%v err=%v want=%d", p, err, tc.want)
			}
		})
	}
}

func TestCouponDoesNotConsumeSubsiteMarkup(t *testing.T) {
	for _, typ := range []couponent.Type{couponent.TypeFixed, couponent.TypePercent} {
		t.Run(string(typ), func(t *testing.T) {
			d, gen, rr, subsite, prod := newJourney(t)
			ctx := tenancy.WithContext(context.Background(), tenancy.Context{SubsiteID: subsite, IsMain: false})
			cp := coupon.NewCouponRepoImpl(d)
			uc := &OrderUsecase{Data: d, Inv: fakeInventory{}, Gen: gen, Reseller: rr, Coupon: cp}
			// Product ¥10 + markup ¥1, quantity 2. A large fixed coupon may
			// consume the product's ¥20, but must leave the ¥2 markup intact.
			d.Client.Coupon.Create().SetName("coupon").SetCode("TEST").SetType(typ).SetValue(5000).SaveX(ctx)
			res, err := uc.CreateOrder(ctx, CreateOrderInput{SubsiteID: subsite, UserID: 2, QueryPassword: "test-only", CouponCode: "TEST", Items: []OrderItemInput{{ProductID: prod.ID, Quantity: 2}}})
			want := int64(200)
			if typ == couponent.TypePercent {
				want = 1200 // 50% of ¥20 + ¥2 markup.
			}
			if err != nil || res.TotalCents != want {
				t.Fatalf("order=%+v err=%v want=%d", res, err, want)
			}
		})
	}
}
