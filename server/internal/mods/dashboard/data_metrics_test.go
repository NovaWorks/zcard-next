package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/platform/businessday"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

func TestDashboardDeletionPaymentRefundAndCost(t *testing.T) {
	d := newDashboardData(t)
	r := NewDashboardRepoImpl(d)
	ctx := context.Background()
	now := businessday.Start(time.Now()).Add(12 * time.Hour).UTC()
	r.now = func() time.Time { return now }
	day := businessday.Start(now)
	o := d.Client.Order.Create().SetOrderNo("paid-yesterday-created").SetTotalAmount(1000).SetStatus(order.StatusPaid).SetCreatedAt(day.Add(-time.Hour)).SetPaidAt(day.Add(time.Hour)).SaveX(ctx)
	d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(1).SetQuantity(2).SetUnitPrice(500).SetAmount(1000).SetCost(300).SetFulfillmentType("auto").SaveX(ctx)
	invalid := d.Client.Order.Create().SetOrderNo("invalid").SetTotalAmount(1000).SetStatus(order.StatusCanceled).SetCreatedAt(day.Add(2 * time.Hour)).SaveX(ctx)
	before, _, _, _, _, _, err := r.GetOverview(ctx)
	if err != nil || before.Orders != 1 || before.Revenue != 1000 || before.PaidOrders != 1 || before.Cost != 600 || before.Profit != 400 {
		t.Fatalf("before=%+v %v", before, err)
	}
	if err := r.RunDailySettle(ctx, day); err != nil {
		t.Fatal(err)
	}
	d.Client.Order.UpdateOneID(invalid.ID).SetAdminDeletedAt(now).SaveX(ctx)
	d.Client.RefundOrder.Create().SetOrderID(o.ID).SetAmount(300).SetChannel("wallet").SetStatus("succeeded").SetCreatedAt(day.Add(3 * time.Hour)).SaveX(ctx)
	for _, status := range []order.Status{order.StatusPaid, order.StatusRefundPending, order.StatusRefunded} {
		d.Client.Order.UpdateOneID(o.ID).SetStatus(status).SaveX(ctx)
		m, _, _, _, _, _, err := r.GetOverview(ctx)
		if err != nil || m.Orders != 0 || m.Revenue != 1000 || m.PaidOrders != 1 || m.Refunds != 300 || m.NetRevenue != 700 || m.Profit != 100 || m.UnknownCostOrders != 0 {
			t.Fatalf("status %s: %+v %v", status, m, err)
		}
	}
	points, err := r.GetTrend(ctx, 1)
	if err != nil || len(points) != 1 || points[0].Orders != 0 || points[0].Revenue != 1000 || points[0].NetRevenue != 700 {
		t.Fatalf("trend=%+v %v", points, err)
	}
	date := businessday.Date(day, "20060102")
	daily, err := r.GetDailyStats(ctx, 0, date, date)
	if err != nil || len(daily) != 1 || daily[0].Orders != 0 || daily[0].Amount != 1000 {
		t.Fatalf("daily=%+v %v", daily, err)
	}
}
func TestDashboardWindowsScopeAndUnknownCost(t *testing.T) {
	d := newDashboardData(t)
	r := NewDashboardRepoImpl(d)
	ctx := context.Background()
	now := businessday.Start(time.Now()).Add(12 * time.Hour).UTC()
	r.now = func() time.Time { return now }
	seedOrder(t, d, 0, "paid", 900, now.AddDate(0, 0, -7).Add(time.Hour)) // outside seven calendar dates
	seedOrder(t, d, 0, "paid", 100, now.Add(-time.Hour))
	seedOrder(t, d, 9, "paid", 500, now.Add(-time.Minute))
	_, _, m, _, _, _, err := r.GetOverview(ctx)
	if err != nil || m.Orders != 1 || m.Revenue != 100 || m.UnknownCostOrders != 1 {
		t.Fatalf("%+v %v", m, err)
	}
	points, err := r.GetTrend(ctx, 7)
	var amount, count int64
	for _, p := range points {
		amount += p.Revenue
		count += p.Orders
	}
	if err != nil || len(points) != 7 || amount != m.Revenue || count != m.Orders {
		t.Fatalf("%+v %v", points, err)
	}
	other := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 9})
	today, _, _, _, _, _, err := r.GetOverview(other)
	if err != nil || today.Revenue != 500 {
		t.Fatalf("subsite=%+v %v", today, err)
	}
	d.Client.Close()
	if _, _, _, _, _, _, err := r.GetOverview(ctx); err == nil {
		t.Fatal("query errors must not become zeros")
	}
}
func TestDashboardChannelIdentityReviewAndRecharge(t *testing.T) {
	d := newDashboardData(t)
	r := NewDashboardRepoImpl(d)
	ctx := context.Background()
	now := time.Now().UTC()
	r.now = func() time.Time { return now }
	old := d.Client.PaymentChannel.Create().SetName("原网关").SetCode("be").SetDriver("bepusdt").SetConfig([]byte("secret")).SetEnabled(false).SetDeletedAt(now).SaveX(ctx)
	fresh := d.Client.PaymentChannel.Create().SetName("新网关").SetCode("be-2").SetDriver("bepusdt").SetConfig([]byte("secret2")).SaveX(ctx)
	makeOrder := func(no string, ch *ent.PaymentChannel, amount int64, tenant uint64) {
		o := d.Client.Order.Create().SetOrderNo(no).SetSubsiteID(tenant).SetStatus(order.StatusPaid).SetPaidAt(now.Add(-time.Minute)).SetTotalAmount(amount).SaveX(ctx)
		d.Client.Payment.Create().SetOrderID(o.ID).SetSubsiteID(tenant).SetChannel(ch.Code).SetChannelID(ch.ID).SetStatus("success").SetAmount(amount).SetChargedAmount(amount).SaveX(ctx)
	}
	makeOrder("old", old, 1000, 0)
	makeOrder("new", fresh, 2000, 0)
	makeOrder("other", fresh, 9999, 9)
	invalid := d.Client.Order.Create().SetOrderNo("deleted-late").SetStatus(order.StatusCanceled).SetAdminDeletedAt(now).SaveX(ctx)
	d.Client.Payment.Create().SetOrderID(invalid.ID).SetChannel(old.Code).SetChannelID(old.ID).SetStatus("success").SetAmount(9000).SetReviewReason("迟到到账").SaveX(ctx)
	d.Client.Payment.Create().SetRechargeOrderID(1).SetChannel(old.Code).SetChannelID(old.ID).SetStatus("success").SetAmount(9000).SaveX(ctx)
	rows, err := r.GetTopChannels(ctx, 7)
	if err != nil || len(rows) != 2 || rows[0].Amount != 2000 || rows[0].Name != "新网关" || rows[1].Amount != 1000 || rows[1].ChannelState != "deleted" {
		t.Fatalf("%+v %v", rows, err)
	}
	n, err := r.PaymentReviewCount(ctx)
	if err != nil || n != 1 {
		t.Fatalf("reviews=%d %v", n, err)
	}
}
func TestDashboardProductAllocationUsesFinalOrderAmount(t *testing.T) {
	d := newDashboardData(t)
	r := NewDashboardRepoImpl(d)
	ctx := context.Background()
	now := time.Now().UTC()
	r.now = func() time.Time { return now }
	o := d.Client.Order.Create().SetOrderNo("coupon").SetTotalAmount(801).SetStatus(order.StatusPaid).SetPaidAt(now.Add(-time.Minute)).SaveX(ctx)
	for _, id := range []uint64{1, 2} {
		d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(id).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetCost(100).SetFulfillmentType("auto").SaveX(ctx)
	}
	rows, err := r.GetTopProducts(ctx, 7)
	if err != nil || len(rows) != 2 || rows[0].Revenue+rows[1].Revenue != 801 {
		t.Fatalf("%+v %v", rows, err)
	}
}

func TestDashboardLateRefundAndMidnight(t *testing.T) {
	d := newDashboardData(t)
	r := NewDashboardRepoImpl(d)
	ctx := context.Background()
	day := businessday.Start(time.Now()).UTC()
	r.now = func() time.Time { return day.Add(12 * time.Hour) }
	o := d.Client.Order.Create().SetOrderNo("old-paid-refunded-today").SetTotalAmount(1000).SetStatus(order.StatusRefunded).
		SetCreatedAt(day.AddDate(0, 0, -90)).SetPaidAt(day.AddDate(0, 0, -89)).SaveX(ctx)
	d.Client.RefundOrder.Create().SetOrderID(o.ID).SetAmount(300).SetChannel("wallet").SetStatus("succeeded").SetCreatedAt(day.Add(time.Hour)).SaveX(ctx)
	d.Client.RefundOrder.Create().SetOrderID(o.ID).SetAmount(200).SetChannel("wallet").SetStatus("processing").SetCreatedAt(day.Add(time.Hour)).SaveX(ctx)
	today, _, _, _, _, _, err := r.GetOverview(ctx)
	if err != nil || today.Revenue != 0 || today.PaidOrders != 0 || today.Refunds != 300 || today.NetRevenue != -300 {
		t.Fatalf("late refund: %+v %v", today, err)
	}
	r.now = func() time.Time { return day }
	points, err := r.GetTrend(ctx, 1)
	if err != nil || len(points) != 1 || points[0].Revenue != 0 || points[0].Refunds != 0 {
		t.Fatalf("midnight: %+v %v", points, err)
	}
}
