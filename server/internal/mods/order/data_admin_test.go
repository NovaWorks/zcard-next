package order

import (
	"context"
	"fmt"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

func TestAdminOrderSearch(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	svc := NewAdminOrderService(uc, d)
	for i, input := range []struct {
		no, contact, guest string
		tenant             uint64
		status             order.Status
	}{
		{"ORDER-needle-1", "", "", 0, order.StatusCanceled},
		{"ORDER-2", "needle@example.com", "", 0, order.StatusCanceled},
		{"ORDER-3", "", "needle-guest", 0, order.StatusPaid},
		{"ORDER-4", "needle-other", "", 7, order.StatusCanceled},
		{"ORDER-5", "unrelated", "", 0, order.StatusCanceled},
	} {
		_, err := d.Client.Order.Create().SetID(uint64(i + 1)).SetOrderNo(input.no).SetContact(input.contact).
			SetGuestContact(input.guest).SetSubsiteID(input.tenant).SetStatus(input.status).Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := svc.ListOrders(ctx, &adminv1.ListOrdersRequest{Keyword: " needle ", Limit: 2})
	if err != nil || len(first.GetOrders()) != 2 || first.NextCursor != 2 {
		t.Fatalf("search first page: %+v %v", first, err)
	}
	second, err := svc.ListOrders(ctx, &adminv1.ListOrdersRequest{Keyword: "needle", Cursor: first.NextCursor, Limit: 2})
	if err != nil || len(second.GetOrders()) != 1 || second.Orders[0].OrderNo != "ORDER-needle-1" {
		t.Fatalf("search next page: %+v %v", second, err)
	}
	filtered, err := svc.ListOrders(ctx, &adminv1.ListOrdersRequest{Keyword: "needle", Status: "paid"})
	if err != nil || len(filtered.GetOrders()) != 1 || filtered.Orders[0].OrderNo != "ORDER-3" {
		t.Fatalf("status and keyword: %+v %v", filtered, err)
	}
	other := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 7})
	result, err := svc.ListOrders(other, &adminv1.ListOrdersRequest{Keyword: "needle"})
	if err != nil || len(result.GetOrders()) != 1 || result.Orders[0].OrderNo != "ORDER-4" {
		t.Fatalf("tenant search: %+v %v", result, err)
	}
}

func TestAdminDeleteOrdersPreservesRecords(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	svc := NewAdminOrderService(uc, d)
	o, err := d.Client.Order.Create().SetOrderNo("CLEAN-1").SetStatus(order.StatusCanceled).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.Payment.Create().SetOrderID(o.ID).SetChannel("test").SetAmount(100).Save(ctx); err != nil {
		t.Fatal(err)
	}
	request := &adminv1.DeleteOrdersRequest{OrderNos: []string{o.OrderNo, o.OrderNo}}
	for i := 0; i < 2; i++ {
		if _, err := svc.DeleteOrders(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := svc.ListOrders(ctx, &adminv1.ListOrdersRequest{Keyword: "CLEAN"})
	if err != nil || len(listed.GetOrders()) != 0 {
		t.Fatalf("deleted order still listed: %+v %v", listed, err)
	}
	kept, err := uc.GetByOrderNo(ctx, o.OrderNo)
	if err != nil || kept.AdminDeletedAt == nil {
		t.Fatalf("source record lost: %+v %v", kept, err)
	}
	if n, err := d.Client.Payment.Query().Where(payment.OrderID(o.ID)).Count(ctx); err != nil || n != 1 {
		t.Fatalf("payment record lost: %d %v", n, err)
	}
	if n, err := d.Client.OrderStatusEvent.Query().Where(orderstatusevent.OrderID(o.ID), orderstatusevent.Event("admin_deleted")).Count(ctx); err != nil || n != 1 {
		t.Fatalf("delete audit must be exactly once: %d %v", n, err)
	}
}

func TestAdminDeleteOrdersRejectsUnsafeBatch(t *testing.T) {
	for _, status := range []order.Status{order.StatusPendingPayment, order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered, order.StatusDelivered, order.StatusCompleted, order.StatusRefundPending, order.StatusRefunded} {
		t.Run(string(status), func(t *testing.T) {
			d, uc, _ := newIdemEnv(t)
			ctx := context.Background()
			a, err := d.Client.Order.Create().SetOrderNo("GOOD").SetStatus(order.StatusExpired).Save(ctx)
			if err != nil {
				t.Fatal(err)
			}
			b, err := d.Client.Order.Create().SetOrderNo("UNSAFE").SetStatus(status).Save(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewAdminOrderService(uc, d).DeleteOrders(ctx, &adminv1.DeleteOrdersRequest{OrderNos: []string{a.OrderNo, b.OrderNo}}); err == nil {
				t.Fatal("unsafe batch accepted")
			}
			if n, err := d.Client.Order.Query().Where(order.AdminDeletedAtNotNil()).Count(ctx); err != nil || n != 0 {
				t.Fatalf("partial deletion: %d %v", n, err)
			}
		})
	}
}

func TestAdminDeleteOrdersGuards(t *testing.T) {
	for _, scenario := range []string{"paid_at", "payment", "tenant", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			d, uc, _ := newIdemEnv(t)
			ctx := context.Background()
			builder := d.Client.Order.Create().SetOrderNo("GUARD").SetStatus(order.StatusCanceled)
			if scenario == "paid_at" {
				builder.SetPaidAt(time.Now())
			}
			if scenario == "tenant" {
				builder.SetSubsiteID(9)
			}
			o, err := builder.Save(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "payment" {
				if _, err := d.Client.Payment.Create().SetOrderID(o.ID).SetChannel("test").SetAmount(100).SetStatus(payment.StatusSuccess).Save(ctx); err != nil {
					t.Fatal(err)
				}
			}
			code := o.OrderNo
			if scenario == "missing" {
				code = "MISSING"
			}
			if _, err := NewAdminOrderService(uc, d).DeleteOrders(ctx, &adminv1.DeleteOrdersRequest{OrderNos: []string{code}}); err == nil {
				t.Fatal("guard failed")
			}
			o, err = d.Client.Order.Get(ctx, o.ID)
			if err != nil || o.AdminDeletedAt != nil {
				t.Fatalf("guard modified order: %+v %v", o, err)
			}
		})
	}
}

func TestAdminOrderListProductNames(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	svc := NewAdminOrderService(uc, d)
	p := d.Client.Product.Create().SetName("视频会员").SetSlug("list-video").SaveX(ctx)
	for _, tenant := range []uint64{0, 7} {
		o := d.Client.Order.Create().SetOrderNo(fmt.Sprintf("list-%d", tenant)).SetSubsiteID(tenant).SaveX(ctx)
		d.Client.OrderItem.Create().SetOrderID(o.ID).SetSubsiteID(tenant).SetProductID(p.ID).SetSkuName("一年版").SetQuantity(2).SetUnitPrice(100).SetAmount(200).SetCost(50).SetFulfillmentType("auto").SaveX(ctx)
	}
	for _, tenant := range []uint64{0, 7} {
		result, err := svc.ListOrders(tenancy.WithContext(ctx, tenancy.Context{SubsiteID: tenant}), &adminv1.ListOrdersRequest{Limit: 20})
		if err != nil || len(result.GetOrders()) != 1 {
			t.Fatal(result, err)
		}
		items := result.Orders[0].Items
		if len(items) != 1 || items[0].Name != "视频会员" || items[0].SkuName != "一年版" || items[0].Quantity != 2 {
			t.Fatal(items)
		}
		if items[0].CostCents != 0 {
			t.Fatal("list summary exposed item cost")
		}
	}
}
