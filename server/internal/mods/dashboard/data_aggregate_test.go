package dashboard

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
)

func TestMetricAggregatePreservesPaidStatusesAndWindow(t *testing.T) {
	d := newDashboardData(t)
	r := NewDashboardRepoImpl(d)
	ctx := context.Background()
	start := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	statuses := []order.Status{order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered, order.StatusDelivered, order.StatusCompleted, order.StatusPendingPayment, order.StatusCanceled, order.StatusExpired, order.StatusRefundPending, order.StatusRefunded}
	for i, status := range statuses {
		d.Client.Order.Create().SetOrderNo(fmt.Sprintf("aggregate-%d", i)).SetSubsiteID(0).SetStatus(status).
			SetTotalAmount(101).SetCost(31).SetBaseCurrency("CNY").SetCreatedAt(start).SetVersion(0).SaveX(ctx)
	}
	seedOrder(t, d, 0, "paid", 999, end) // exclusive end
	seedOrder(t, d, 0, "paid", 999, start.Add(-time.Nanosecond))
	seedOrder(t, d, 9, "paid", 999, start.Add(time.Second))
	m, err := r.metricBetweenSubsite(ctx, 0, start, end)
	if err != nil || m.Orders != 10 || m.PaidOrders != 5 || m.Revenue != 505 || m.Cost != 155 || m.Profit != 350 {
		t.Fatalf("aggregate=%+v err=%v", m, err)
	}
	empty, err := r.metricBetweenSubsite(ctx, 88, start, end)
	if err != nil || empty != (Metric{}) {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
}
