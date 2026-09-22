package order

import (
	"context"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"testing"
	"time"
)

func TestDashboardOrderDrilldownMatchesWindowAndScope(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	svc := NewAdminOrderService(uc, d)
	start := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	for i := 0; i < 5; i++ {
		b := d.Client.Order.Create().SetOrderNo(fmt.Sprintf("filter-%d", i)).SetStatus(order.StatusPaid).SetTotalAmount(100).SetCreatedAt(start.Add(-time.Hour)).SetPaidAt(start.Add(time.Minute))
		if i == 2 {
			b.SetPaidAt(end)
		}
		if i == 3 {
			b.SetSubsiteID(9)
		}
		if i == 4 {
			b.SetAdminDeletedAt(start)
		}
		o := b.SaveX(ctx)
		d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(7).SetQuantity(1).SetUnitPrice(100).SetAmount(100).SetFulfillmentType("auto").SaveX(ctx)
		d.Client.Payment.Create().SetOrderID(o.ID).SetSubsiteID(o.SubsiteID).SetChannel("be").SetChannelID(88).SetStatus("success").SetAmount(100).SaveX(ctx)
	}
	req := &adminv1.ListOrdersRequest{StartTime: start.Unix(), EndTime: end.Unix(), TimeField: "paid", ChannelId: 88, ProductId: 7, Limit: 1}
	first, err := svc.ListOrders(ctx, req)
	if err != nil || len(first.Orders) != 1 || first.Orders[0].OrderNo != "filter-1" {
		t.Fatalf("first=%+v %v", first, err)
	}
	req.Cursor = first.NextCursor
	second, err := svc.ListOrders(ctx, req)
	if err != nil || len(second.Orders) != 1 || second.Orders[0].OrderNo != "filter-0" {
		t.Fatalf("second=%+v %v", second, err)
	}
	req.Cursor = 0
	req.TimeField = "created"
	rows, err := svc.ListOrders(ctx, req)
	if err != nil || len(rows.Orders) != 0 {
		t.Fatalf("created=%+v %v", rows, err)
	}
	req.TimeField = "paid"
	other := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 9})
	rows, err = svc.ListOrders(other, req)
	if err != nil || len(rows.Orders) != 1 || rows.Orders[0].OrderNo != "filter-3" {
		t.Fatalf("tenant=%+v %v", rows, err)
	}
	req.EndTime = req.StartTime
	if _, err := svc.ListOrders(ctx, req); err == nil {
		t.Fatal("invalid time window accepted")
	}
}
