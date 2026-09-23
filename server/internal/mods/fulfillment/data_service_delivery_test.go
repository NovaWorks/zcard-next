package fulfillment

import (
	"bytes"
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"testing"
)

func TestManualServiceLifecycleAndActorIsolation(t *testing.T) {
	d, cipher, repo := newFulfillData(t)
	ctx := context.Background()
	_, o := seedPaidOrderWithCards(t, d, cipher, "status", 2)
	it := d.Client.OrderItem.Query().Where(orderitem.OrderID(o.ID)).OnlyX(ctx)
	d.Client.OrderItem.UpdateOneID(it.ID).SetFulfillmentType("manual").SetProductName("频道服务").ExecX(ctx)
	svc := NewAdminFulfillmentService(repo, d)
	actor := identity.WithClaims(ctx, &authn.Claims{Subject: 7})
	req := &adminv1.StartServiceRequest{OrderNo: o.OrderNo, OrderItemId: it.ID}
	if err := repo.CompleteService(actor, o.OrderNo, it.ID, "已完成", "", 7); err == nil {
		t.Fatal("completion before claim accepted")
	}
	if _, err := svc.StartService(actor, req); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartService(actor, req); err != nil {
		t.Fatal("claim retry", err)
	}
	other := identity.WithClaims(ctx, &authn.Claims{Subject: 8})
	if _, err := svc.StartService(other, req); err == nil {
		t.Fatal("other actor stole work")
	}
	rf := d.Client.RefundOrder.Create().SetOrderID(o.ID).SetAmount(100).SetChannel("wallet").SetStatus("processing").SaveX(ctx)
	if err := repo.CompleteService(actor, o.OrderNo, it.ID, "频道已完成", "内部备注", 7); err == nil {
		t.Fatal("delivered during refund")
	}
	d.Client.RefundOrder.UpdateOneID(rf.ID).SetStatus("failed").ExecX(ctx)
	if err := repo.CompleteService(actor, o.OrderNo, it.ID, "频道已完成", "内部备注", 7); err != nil {
		t.Fatal(err)
	}
	delivery := d.Client.OrderDelivery.Query().OnlyX(ctx)
	if bytes.Contains(delivery.ServiceContent, []byte("频道已完成")) || delivery.DeliveredQuantity != 2 || delivery.CardID != 0 {
		t.Fatal("result not encrypted or incorrectly represented as cards")
	}
	if got := repo.serviceText(ctx, delivery); got != "频道已完成" {
		t.Fatal(got)
	}
	if d.Client.Order.GetX(ctx, o.ID).Status != "completed" || d.Client.OrderItem.GetX(ctx, it.ID).FulfillmentStatus != "delivered" {
		t.Fatal("service still waits for user to fetch")
	}
	if err := repo.CompleteService(actor, o.OrderNo, it.ID, "第二次", "", 7); err == nil {
		t.Fatal("duplicate completion accepted")
	}
	if d.Client.OrderDelivery.Query().CountX(ctx) != 1 {
		t.Fatal("duplicate deliveries")
	}
	if d.Client.OutboxEvent.Query().CountX(ctx) != 1 {
		t.Fatal("completion notification missing")
	}
}
