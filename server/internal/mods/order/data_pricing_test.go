package order

import (
	"context"
	"testing"

	orderent "github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderamountline"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

func TestDiscountRatesArePayableProportions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		input         PriceInput
		total         money.Cents
		member, group int64
	}{
		{"98 percent member", PriceInput{BasePrice: 3600, Quantity: 1, MemberRate: 9800}, 3528, -72, 0},
		{"98 percent group", PriceInput{BasePrice: 3600, Quantity: 1, GroupRate: 9800}, 3528, 0, -72},
		{"95 percent member", PriceInput{BasePrice: 3600, Quantity: 1, MemberRate: 9500}, 3420, -180, 0},
		{"10 percent member", PriceInput{BasePrice: 3600, Quantity: 1, MemberRate: 1000}, 360, -3240, 0},
		{"no matching discounts", PriceInput{BasePrice: 3600, Quantity: 1}, 3600, 0, 0},
		{"100 percent payable", PriceInput{BasePrice: 3600, Quantity: 1, MemberRate: 10000, GroupRate: 10000}, 3600, 0, 0},
		{"sequential discounts", PriceInput{BasePrice: 3600, Quantity: 1, MemberRate: 9800, GroupRate: 9500}, 3352, -72, -176},
		{"multiple items", PriceInput{BasePrice: 3600, Quantity: 3, MemberRate: 9800}, 10584, -72, 0},
		{"fractional cent discount is not deducted", PriceInput{BasePrice: 199, Quantity: 1, MemberRate: 9500}, 190, -9, 0},
		{"one cent is not made free", PriceInput{BasePrice: 1, Quantity: 1, MemberRate: 9800, GroupRate: 9500}, 1, 0, 0},
		{"coupon after member discount", PriceInput{BasePrice: 3600, Quantity: 1, MemberRate: 9800, CouponValue: 528}, 3000, -72, 0},
		{"flash after member discount", PriceInput{BasePrice: 3600, Quantity: 1, MemberRate: 9800, FlashPrice: 3000}, 3000, -72, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := PriceCalculator(tc.input)
			if got.Total != tc.total {
				t.Fatalf("payable = %d, want %d", got.Total, tc.total)
			}
			if ValidateTotal(got.Lines).Mul(tc.input.Quantity) != got.Total {
				t.Fatal("amount lines do not reconcile")
			}
			discounts := map[string]int64{}
			for _, line := range got.Lines {
				discounts[line.Type] += line.Amount
			}
			if discounts["member_discount"] != tc.member || discounts["group_discount"] != tc.group {
				t.Fatalf("incorrect discount lines: %v", discounts)
			}
		})
	}
}

func TestCreateOrderDiscountSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name          string
		user          uint64
		member, group int32
		want          int64
		line          string
	}{
		{"member 98 percent", 3, 9800, 0, 3528, "member_discount"},
		{"group 98 percent", 3, 0, 9800, 3528, "group_discount"},
		{"guest without group", 0, 9800, 0, 3600, ""},
		{"member no discount", 3, 10000, 0, 3600, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, uc, payRepo := newIdemEnv(t)
			ctx := context.Background()
			d.Client.Product.UpdateOneID(1).SetPrice(3600).ExecX(ctx)
			levels := memberlevel.NewMemberLevelRepoImpl(d, nil)
			if _, err := levels.CreateLevel(ctx, "member", "consume", 0, 0, tc.member, 1, true, nil); err != nil {
				t.Fatal(err)
			}
			uc.MemberRate = levels
			uc.Catalog = catalog.NewProductRepoImpl(d, nil)
			if tc.group > 0 {
				d.Client.MemberProductGroup.Create().SetName("group").SetProductIds([]uint64{1}).SetDiscount(tc.group).SaveX(ctx)
			}
			// Existing pending orders must retain the amount agreed at their creation.
			old := d.Client.Order.Create().SetOrderNo("OLD-LOW-PRICE").SetTotalAmount(72).SaveX(ctx)
			res, err := uc.CreateOrder(ctx, CreateOrderInput{UserID: tc.user, Contact: "test@example.invalid", QueryPassword: "test-only-password", Items: []OrderItemInput{{ProductID: 1, Quantity: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			o := d.Client.Order.Query().Where(orderent.OrderNo(res.OrderNo)).OnlyX(ctx)
			item := d.Client.OrderItem.Query().Where(orderitem.OrderID(o.ID)).OnlyX(ctx)
			if res.TotalCents != tc.want || o.TotalAmount != tc.want || item.UnitPrice != 3600 || item.Amount != tc.want {
				t.Fatalf("reply=%d order=%d unit=%d item=%d want=%d", res.TotalCents, o.TotalAmount, item.UnitPrice, item.Amount, tc.want)
			}
			lines := d.Client.OrderAmountLine.Query().Where(orderamountline.OrderID(o.ID)).AllX(ctx)
			var sum int64
			for _, line := range lines {
				sum += line.Amount
				if tc.line != "" && string(line.Type) == tc.line && line.Amount != -72 {
					t.Fatalf("discount line=%d", line.Amount)
				}
			}
			if sum != tc.want {
				t.Fatalf("amount lines sum=%d want=%d", sum, tc.want)
			}
			d.Client.PaymentChannel.Create().SetName("test epay").SetCode("test-epay").SetDriver("epay").SetConfig([]byte("{}")).SetEnabled(true).SaveX(ctx)
			payment, err := payRepo.CreatePayment(ctx, o.ID, "test-epay", o.TotalAmount, "")
			if err != nil {
				t.Fatal(err)
			}
			if payment.Amount != tc.want {
				t.Fatalf("payment snapshot=%d want=%d", payment.Amount, tc.want)
			}
			if d.Client.Order.GetX(ctx, old.ID).TotalAmount != 72 {
				t.Fatal("existing order repriced")
			}
		})
	}
}
