package payment

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"testing"
	"time"
)

func TestExpiryChannelIdentityAndDeadline(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	ch := d.Client.PaymentChannel.Create().SetCode("checkout").SetName("epay").SetDriver("epay").SetConfig([]byte("{}")).SaveX(ctx)
	d.Client.PaymentChannel.Create().SetSubsiteID(7).SetCode("checkout").SetName("usdt").SetDriver("epusdt").SetConfig([]byte("{}")).SaveX(ctx)
	o, _ := seedPendingOrder(t, d, "checkout", 100)
	p, err := repo.CreatePayment(ctx, o.ID, "checkout", 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.ChannelID != ch.ID || p.DriverSnapshot != "epay" {
		t.Fatal("wrong channel snapshot")
	}
	slow, err := repo.HasPendingSlowPayment(ctx, o.ID)
	if err != nil || slow {
		t.Fatalf("epay mistaken for other tenant usdt: %v", err)
	}
	d.Client.PaymentChannel.UpdateOneID(ch.ID).SetDriver("epusdt").SaveX(ctx)
	// New immutable snapshot still identifies epay, while legacy ambiguity must
	// not resolve across tenants. Remove the legacy attempt for this assertion.
	d.Client.Payment.Delete().Where(payment.OrderID(o.ID), payment.IDNEQ(p.ID)).ExecX(ctx)
	slow, err = repo.HasPendingSlowPayment(ctx, o.ID)
	if err != nil || slow {
		t.Fatal("snapshot changed with channel")
	}
	d.Client.Order.UpdateOneID(o.ID).SetExpiredAt(time.Now().UTC().Add(-time.Minute)).SaveX(ctx)
	if _, err = repo.CreatePayment(ctx, o.ID, "checkout", 100, ""); err == nil {
		t.Fatal("created attempt after deadline")
	}
}

func TestLatePaymentRetainedWithoutReopeningOrder(t *testing.T) {
	d, repo, _, _, life, _ := newCallbackEnv(t)
	ctx := context.Background()
	o, p := seedPendingOrder(t, d, "epay", 100)
	d.Client.Order.UpdateOneID(o.ID).SetStatus(order.StatusCanceled).SaveX(ctx)
	fact := CallbackFact{Channel: "epay", OrderNo: o.OrderNo, ChannelOrderNo: "late", Amount: 100, Currency: "CNY", Success: true}
	for i := 0; i < 2; i++ {
		if err := repo.HandleCallback(ctx, p.ID, fact); err != nil {
			t.Fatal(err)
		}
	}
	got := d.Client.Payment.GetX(ctx, p.ID)
	if got.Status != payment.StatusSuccess || got.ReviewReason == "" {
		t.Fatal("late payment lost")
	}
	if d.Client.Order.GetX(ctx, o.ID).Status != order.StatusCanceled || len(life.markPaidCalls) != 0 {
		t.Fatal("late payment reopened order")
	}
}
