package payment

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	couponent "github.com/NovaWorks/zcard-next/server/internal/data/ent/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon"
	ordermod "github.com/NovaWorks/zcard-next/server/internal/mods/order"
)

func seedCouponRefund(t *testing.T) (*data.Data, *PaymentRepoImpl, *ent.Order, *ent.OrderItem, *ent.Coupon) {
	t.Helper()
	ctx := context.Background()
	d, r, _, _, _, _ := newCallbackEnv(t)
	r.outbox = data.NewOutboxWriter(d)
	r.lifecycle = ordermod.ProvideOrderLifecycle(&ordermod.OrderUsecase{Coupon: coupon.NewCouponRepoImpl(d)})
	p := d.Client.Product.Create().SetName("Parcel").SetSlug("coupon-parcel").SetPrice(500).SetGoodsType("physical").SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("COUPON-REFUND").SetUserID(1).SetCommerceVersion(1).SetStatus("paid").SetTotalAmount(1000).SetShippingAmount(100).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(2).SetUnitPrice(500).SetAmount(1000).SetPaidAmount(900).SetShippingAmount(100).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	c := d.Client.Coupon.Create().SetName("Coupon").SetCode("REFUND-COUPON").SetType("fixed").SetValue(100).SetUserID(1).SetStatus(couponent.StatusUsed).SetUsedAt(time.Now().UTC()).SetUsedOrderID(o.ID).SaveX(ctx)
	return d, r, o, it, c
}

func couponRefundRequest(it *ent.OrderItem, amount, shipping, alreadyRefunded int64, canceled int32, key string) *adminv1.CreateRefundRequest {
	return &adminv1.CreateRefundRequest{Channel: "wallet", RequestKey: key, AmountCents: amount + shipping, ExpectedRefundedCents: refundPtr(alreadyRefunded), ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"amount_cents":%d,"shipping_cents":%d,"cancel_quantity":%d}]`, it.ID, amount, shipping, canceled)}
}

func TestPhysicalFullUnshippedRefundReturnsCouponThroughLifecycle(t *testing.T) {
	d, r, o, it, c := seedCouponRefund(t)
	ctx := context.Background()
	// Payment fees have their own refund accounting and do not affect coupon return.
	d.Client.Payment.Create().SetOrderID(o.ID).SetChannel("epay").SetStatus("success").SetAmount(1050).SetFee(50).SaveX(ctx)
	req := couponRefundRequest(it, 900, 100, 0, 2, "full-coupon-refund")
	first, err := r.RefundPhysical(ctx, o.ID, 7, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Client.Coupon.GetX(ctx, c.ID); got.Status != couponent.StatusUnused || got.UsedOrderID != 0 || !got.UsedAt.IsZero() || got.UserID != 1 {
		t.Fatal("full unshipped refund did not restore the owner's coupon")
	}
	// Reusing the returned coupon must survive an old refund request replay.
	if err := coupon.NewCouponRepoImpl(d).MarkUsed(ctx, c.ID, o.ID+1); err != nil {
		t.Fatal(err)
	}
	replayed, err := r.RefundPhysical(ctx, o.ID, 7, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Client.Coupon.GetX(ctx, c.ID); got.UsedOrderID != o.ID+1 || got.Status != couponent.StatusUsed || first.ID != replayed.ID || d.Client.RefundOrder.Query().CountX(ctx) != 1 {
		t.Fatal("refund replay revoked a newer coupon use or duplicated the refund")
	}
}

func TestPhysicalPartialRefundKeepsCouponUntilFinalUnshippedRefund(t *testing.T) {
	d, r, o, it, c := seedCouponRefund(t)
	ctx := context.Background()
	if _, err := r.RefundPhysical(ctx, o.ID, 7, couponRefundRequest(it, 450, 0, 0, 1, "partial-coupon-refund")); err != nil {
		t.Fatal(err)
	}
	if d.Client.Coupon.GetX(ctx, c.ID).Status != couponent.StatusUsed {
		t.Fatal("partial refund returned the coupon")
	}
	if _, err := r.RefundPhysical(ctx, o.ID, 7, couponRefundRequest(it, 450, 100, 450, 1, "final-coupon-refund")); err != nil {
		t.Fatal(err)
	}
	if d.Client.Coupon.GetX(ctx, c.ID).Status != couponent.StatusUnused {
		t.Fatal("cumulative full cancellation did not return the coupon")
	}
}

func TestPhysicalRefundKeepsExpiredOrShippedCoupon(t *testing.T) {
	for _, state := range []string{"expired", "shipped", "virtual_delivered"} {
		t.Run(state, func(t *testing.T) {
			d, r, o, it, c := seedCouponRefund(t)
			ctx := context.Background()
			cancel := int32(2)
			if state == "expired" {
				d.Client.Coupon.UpdateOneID(c.ID).SetExpireAt(time.Now().UTC().Add(-time.Hour)).ExecX(ctx)
			}
			if state == "shipped" {
				d.Client.OrderItem.UpdateOneID(it.ID).SetShippedQuantity(1).SetFulfillmentStatus("shipped").ExecX(ctx)
				cancel = 1
			}
			if state == "virtual_delivered" {
				d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(it.ProductID).SetQuantity(1).SetUnitPrice(0).SetAmount(0).SetPaidAmount(0).SetGoodsType("virtual").SetFulfillmentType("manual").SetFulfillmentStatus("delivered").SaveX(ctx)
			}
			if _, err := r.RefundPhysical(ctx, o.ID, 7, couponRefundRequest(it, 900, 100, 0, cancel, "unreturnable-coupon-refund")); err != nil {
				t.Fatal(err)
			}
			if got := d.Client.Coupon.GetX(ctx, c.ID); got.Status != couponent.StatusUsed || got.UsedOrderID != o.ID {
				t.Fatal("expired or delivered order coupon was returned")
			}
		})
	}
}

func TestPhysicalZeroTotalCancellationReturnsFullyDiscountedCoupon(t *testing.T) {
	d, r, o, it, c := seedCouponRefund(t)
	ctx := context.Background()
	d.Client.Order.UpdateOneID(o.ID).SetTotalAmount(0).SetShippingAmount(0).ExecX(ctx)
	d.Client.OrderItem.UpdateOneID(it.ID).SetPaidAmount(0).SetShippingAmount(0).ExecX(ctx)
	d.Client.Coupon.UpdateOneID(c.ID).SetValue(1000).ExecX(ctx)
	if _, err := r.RefundPhysical(ctx, o.ID, 7, couponRefundRequest(it, 0, 0, 0, 2, "zero-total-coupon-cancel")); err != nil {
		t.Fatal(err)
	}
	if d.Client.Coupon.GetX(ctx, c.ID).Status != couponent.StatusUnused {
		t.Fatal("fully discounted, canceled order retained its coupon")
	}
}

type failingCouponReturnLifecycle struct{ fakeLifecycle }

func (f *failingCouponReturnLifecycle) ReturnCoupons(context.Context, uint64) error {
	return errors.New("coupon return unavailable")
}

func TestPhysicalCouponReturnFailureRollsBackRefund(t *testing.T) {
	d, r, o, it, c := seedCouponRefund(t)
	r.lifecycle = &failingCouponReturnLifecycle{}
	ctx := context.Background()
	if _, err := r.RefundPhysical(ctx, o.ID, 7, couponRefundRequest(it, 900, 100, 0, 2, "failed-coupon-refund")); err == nil {
		t.Fatal("refund succeeded after coupon return failed")
	}
	if d.Client.Order.GetX(ctx, o.ID).Status != "paid" || d.Client.OrderItem.GetX(ctx, it.ID).CanceledQuantity != 0 || d.Client.Product.GetX(ctx, it.ProductID).PhysicalStock != 0 || d.Client.RefundOrder.Query().CountX(ctx) != 0 || d.Client.WalletTransaction.Query().CountX(ctx) != 0 || d.Client.Coupon.GetX(ctx, c.ID).Status != couponent.StatusUsed {
		t.Fatal("coupon return failure left refund, stock, wallet or order changes")
	}
}
