package reseller

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"testing"
	"time"
)

func TestPhysicalMarkupRefundBeforeAndAfterConfirmation(t *testing.T) {
	r, d := newResellerData(t)
	ctx := context.Background()
	site := seedApproved(t, r, d)
	s := NewSettleService(r, nil)
	o := d.Client.Order.Create().SetOrderNo("physical-markup").SetSubsiteID(site).SetCommerceVersion(1).SetTotalAmount(1300).SetShippingAmount(300).SetSubsiteProfit(100).SetProfitEligible(true).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(1).SetQuantity(1).SetUnitPrice(1000).SetAmount(1000).SetPaidAmount(1000).SetProfitSnapshot(map[string]any{"markup": 100}).SetFulfillmentType("shipping").SaveX(ctx)
	paid := events.Envelope{Payload: []byte(fmt.Sprintf(`{"order_id":%d,"subsite_id":%d,"subsite_profit":100,"profit_eligible":true}`, o.ID, site))}
	refund := events.Envelope{Payload: []byte(fmt.Sprintf(`{"order_id":%d}`, o.ID))}
	d.Client.OrderItem.UpdateOneID(it.ID).SetRefundedAmount(400).ExecX(ctx)
	if e := s.OnOrderRefunded(ctx, refund); e != nil {
		t.Fatal(e)
	}
	if e := s.OnOrderPaid(ctx, paid); e != nil {
		t.Fatal(e)
	}
	if e := s.OnOrderPaid(ctx, paid); e != nil {
		t.Fatal(e)
	}
	a, _, debt, e := r.RecomputeBalance(ctx, site)
	if e != nil || a != 60 || debt != 0 {
		t.Fatalf("pending refund balance %d debt %d %v", a, debt, e)
	}
	if _, e := r.ConfirmDue(ctx, time.Now().UTC().AddDate(0, 0, 8), 100); e != nil {
		t.Fatal(e)
	}
	d.Client.OrderItem.UpdateOneID(it.ID).SetRefundedAmount(700).ExecX(ctx)
	for i := 0; i < 2; i++ {
		if e := s.OnOrderRefunded(ctx, refund); e != nil {
			t.Fatal(e)
		}
	}
	a, _, debt, e = r.RecomputeBalance(ctx, site)
	if e != nil || a != 30 || debt != 0 {
		t.Fatalf("confirmed refund balance %d debt %d %v", a, debt, e)
	}
	balance, e := r.GetBalance(ctx, site)
	if e != nil || balance.Available != 30 {
		t.Fatal("cached balance mismatch", balance, e)
	}
}
