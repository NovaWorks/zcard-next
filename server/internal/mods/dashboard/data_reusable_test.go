package dashboard

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/platform/businessday"
	"testing"
	"time"
)

func TestSharedPurchaseCostCountedOnceAcrossBuyersAndRefund(t *testing.T) {
	d := newDashboardData(t)
	r := NewDashboardRepoImpl(d)
	ctx := context.Background()
	now := businessday.Start(time.Now()).Add(12 * time.Hour).UTC()
	r.now = func() time.Time { return now }
	src := d.Client.ProductDeliverySource.Create().SetProductID(1).SetConnectionID(1).SetStatus("ready").SetSubmittedAt(now.Add(-time.Hour).Unix()).SetCostCents(120).SaveX(ctx)
	for i := 0; i < 3; i++ {
		o := d.Client.Order.Create().SetOrderNo(fmt.Sprintf("share-%d", i)).SetStatus("paid").SetPaidAt(now.Add(-time.Hour)).SetCreatedAt(now.Add(-time.Hour)).SetTotalAmount(100).SaveX(ctx)
		d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(1).SetQuantity(1).SetUnitPrice(100).SetAmount(100).SetCost(0).SetFulfillmentType("reuse").SetDeliverySourceID(src.ID).SaveX(ctx)
		if i == 0 {
			d.Client.RefundOrder.Create().SetOrderID(o.ID).SetAmount(100).SetStatus("succeeded").SetChannel("wallet").SetCreatedAt(now.Add(-time.Minute)).SaveX(ctx)
		}
	}
	m, _, _, _, _, _, e := r.GetOverview(ctx)
	if e != nil || m.Cost != 120 || m.Profit != 80 || m.UnknownCostOrders != 0 {
		t.Fatalf("shared costs repeated or lost: %+v %v", m, e)
	}
	d.Client.ProductDeliverySource.UpdateOneID(src.ID).ClearCurrentKey().SetStatus("paused").ExecX(ctx)
	m, _, _, _, _, _, e = r.GetOverview(ctx)
	if e != nil || m.Cost != 120 {
		t.Fatal("retiring source erased purchase expense")
	}
	// A manually entered account may have been bought outside this system.
	// Its absent purchase record must not be silently treated as zero cost.
	d.Client.ProductDeliverySource.UpdateOneID(src.ID).SetConnectionID(0).ExecX(ctx)
	m, _, _, _, _, _, e = r.GetOverview(ctx)
	if e != nil || m.UnknownCostOrders != 3 || m.Profit != 0 {
		t.Fatalf("manual asset treated as free: %+v %v", m, e)
	}
}
