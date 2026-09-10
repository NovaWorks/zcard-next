package fulfillment

import (
	"context"
	"errors"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

func TestGuestUpstreamManualRecovery(t *testing.T) {
	d, cipher, r := newFulfillData(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("upstream").SetSlug("upstream").SetPrice(1000).SaveX(ctx)
	password, _ := crypto.HashPassword("guest-password")
	o := d.Client.Order.Create().SetOrderNo("GUEST-RECOVERY").SetStatus(order.StatusPaid).SetTotalAmount(2000).SetQueryPasswordHash(password).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(2).SetUnitPrice(1000).SetAmount(2000).SetFulfillmentType(orderitem.FulfillmentTypeUpstream).SaveX(ctx)
	po := d.Client.ProcurementOrder.Create().SetOrderItemID(it.ID).SetConnectionID(1).SetDedupeKey("guest-recovery").SetStatus(procurementorder.StatusManual).SetLastError("上游余额不足").SaveX(ctx)
	if err := r.FulfillOrder(ctx, o.OrderNo); err != nil {
		t.Fatal(err)
	}
	pending, err := r.ListPending(ctx, 1, 20)
	if err != nil || len(pending) != 1 {
		t.Fatalf("missing pending order: %v", err)
	}
	got, err := r.FetchDelivery(ctx, o.OrderNo, "guest-password", "127.0.0.1")
	if err != nil || got.Status != "fulfilling" || len(got.Items) != 0 {
		t.Fatalf("empty pending result: %#v %v", got, err)
	}
	if d.Client.OrderStatusEvent.Query().Where(orderstatusevent.Event("completed")).CountX(ctx) != 0 {
		t.Fatal("false completed event")
	}
	if err := r.ManualDeliver(ctx, o.OrderNo, "ONE", "", "补发", 1, it.ID); err == nil {
		t.Fatal("wrong quantity accepted")
	}
	if err := r.ManualDeliver(ctx, o.OrderNo, "ONE\nTWO", "", "补发", 1, it.ID); err != nil {
		t.Fatal(err)
	}
	if d.Client.ProcurementOrder.GetX(ctx, po.ID).Status != procurementorder.StatusFulfilled {
		t.Fatal("procurement was not resolved")
	}
	if err := r.ManualDeliver(ctx, o.OrderNo, "ONE\nTWO", "", "", 1, it.ID); err == nil {
		t.Fatal("duplicate accepted")
	}
	got, err = r.FetchDelivery(ctx, o.OrderNo, "guest-password", "127.0.0.1")
	if err != nil || got.Status != "completed" || len(got.Items) != 2 || got.Items[0].Content != "ONE" {
		t.Fatalf("guest cannot fetch: %#v %v", got, err)
	}
	if _, err := r.FetchDelivery(ctx, o.OrderNo, "wrong", "127.0.0.1"); err == nil {
		t.Fatal("wrong password accepted")
	}
	for _, c := range d.Client.Card.Query().AllX(ctx) {
		if string(c.Content) == "ONE" || string(c.Content) == "TWO" {
			t.Fatal("plaintext stored")
		}
		if _, err := cipher.Open(c.Content, p.ID, 0); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMixedDeliveryDoesNotCompleteOnPartialFetch(t *testing.T) {
	d, cipher, r := newFulfillData(t)
	ctx := context.Background()
	_, o := seedPaidOrderWithCards(t, d, cipher, "status", 1)
	p := d.Client.Product.Create().SetName("upstream").SetSlug("upstream").SetPrice(1000).SetUpstreamSourceID(1).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(1000).SetAmount(1000).SetFulfillmentType(orderitem.FulfillmentTypeUpstream).SaveX(ctx)
	// Upstream can finish before the local fulfillment consumer runs.
	sealed, _ := cipher.Seal("UP", p.ID, o.SubsiteID)
	if err := r.AttachUpstreamDelivery(ctx, o.ID, it.ID, p.ID, []port.UpstreamDeliveryItem{{SealedContent: sealed, ContentHash: cipher.ContentHash("UP")}}); err != nil {
		t.Fatal(err)
	}
	if d.Client.Order.GetX(ctx, o.ID).Status != order.StatusPartiallyDelivered {
		t.Fatal("local item was skipped")
	}
	if _, err := r.FetchDelivery(ctx, o.OrderNo, "", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if d.Client.OrderStatusEvent.Query().Where(orderstatusevent.Event("completed")).CountX(ctx) != 0 {
		t.Fatal("partial fetch recorded completion")
	}
	if err := r.FulfillOrder(ctx, o.OrderNo); err != nil {
		t.Fatal(err)
	}
	if d.Client.Order.GetX(ctx, o.ID).Status != order.StatusDelivered {
		t.Fatal("local delivery did not finish")
	}
	got, err := r.FetchDelivery(ctx, o.OrderNo, "", "127.0.0.1")
	if err != nil || got.Status != "completed" || len(got.Items) != 2 {
		t.Fatalf("completion after early fetch: %#v %v", got, err)
	}
	if d.Client.OrderStatusEvent.Query().Where(orderstatusevent.Event("completed")).CountX(ctx) != 1 {
		t.Fatal("completion audit mismatch")
	}
}

func TestManualDeliveryRollbackAndStateGuards(t *testing.T) {
	for _, status := range []order.Status{order.StatusPendingPayment, order.StatusRefunded, order.StatusCanceled, order.StatusDelivered, order.StatusCompleted} {
		t.Run(string(status), func(t *testing.T) {
			d, cipher, r := newFulfillData(t)
			ctx := context.Background()
			_, o := seedPaidOrderWithCards(t, d, cipher, "status", 1)
			d.Client.Order.UpdateOneID(o.ID).SetStatus(status).SaveX(ctx)
			if err := r.ManualDeliver(ctx, o.OrderNo, "CARD", "", "", 1); err == nil {
				t.Fatal("invalid state accepted")
			}
			if d.Client.OrderDelivery.Query().CountX(ctx) != 0 {
				t.Fatal("delivery created")
			}
		})
	}
	d, cipher, r := newFulfillData(t)
	ctx := context.Background()
	_, o := seedPaidOrderWithCards(t, d, cipher, "status", 1)
	d.Client.OrderStatusEvent.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, errors.New("audit unavailable") })
	})
	before := d.Client.Card.Query().CountX(ctx)
	if err := r.ManualDeliver(ctx, o.OrderNo, "CARD", "", "", 1); err == nil {
		t.Fatal("audit failure hidden")
	}
	if d.Client.OrderDelivery.Query().CountX(ctx) != 0 || d.Client.Card.Query().CountX(ctx) != before || d.Client.Order.GetX(ctx, o.ID).Status != order.StatusPaid {
		t.Fatal("partial writes survived rollback")
	}
}

func TestManualRecoveryOldUnexecutedRefund(t *testing.T) {
	d, cipher, r := newFulfillData(t)
	ctx := context.Background()
	_, o := seedPaidOrderWithCards(t, d, cipher, "status", 1)
	it := d.Client.OrderItem.Query().OnlyX(ctx)
	po := d.Client.ProcurementOrder.Create().SetOrderItemID(it.ID).SetConnectionID(1).SetDedupeKey("legacy").SetStatus(procurementorder.StatusRefunding).SaveX(ctx)
	rf := d.Client.RefundOrder.Create().SetOrderID(o.ID).SetAmount(1000).SetChannel(refundorder.ChannelUpstream).SetStatus(refundorder.StatusCreated).SaveX(ctx)
	if err := r.ManualDeliver(ctx, o.OrderNo, "MANUAL", "", "", 1, it.ID); err != nil {
		t.Fatal(err)
	}
	if d.Client.RefundOrder.GetX(ctx, rf.ID).Status != refundorder.StatusFailed || d.Client.ProcurementOrder.GetX(ctx, po.ID).Status != procurementorder.StatusFulfilled {
		t.Fatal("legacy unresolved")
	}
}

func TestManualMultipleItemsAndConcurrentRetry(t *testing.T) {
	d, _, r := newFulfillData(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("manual").SetSlug("manual").SetPrice(1000).SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("MULTI-MANUAL").SetStatus(order.StatusFulfilling).SetTotalAmount(3000).SaveX(ctx)
	a := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(1000).SetAmount(1000).SetFulfillmentType(orderitem.FulfillmentTypeManual).SaveX(ctx)
	b := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(2).SetUnitPrice(1000).SetAmount(2000).SetFulfillmentType(orderitem.FulfillmentTypeManual).SaveX(ctx)
	if err := r.ManualDeliver(ctx, o.OrderNo, "A", "", "", 1); err == nil {
		t.Fatal("ambiguous item accepted")
	}
	if err := r.ManualDeliver(ctx, o.OrderNo, "A", "", "", 1, b.ID+999); err == nil {
		t.Fatal("foreign item accepted")
	}
	if err := r.ManualDeliver(ctx, o.OrderNo, "A", "", "", 1, a.ID); err != nil {
		t.Fatal(err)
	}
	if d.Client.Order.GetX(ctx, o.ID).Status != order.StatusPartiallyDelivered {
		t.Fatal("multi-item prematurely delivered")
	}
	if _, err := r.FetchDelivery(ctx, o.OrderNo, "", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { results <- r.ManualDeliver(ctx, o.OrderNo, "B\nC", "", "", 1, b.ID) }()
	}
	successes := 0
	for i := 0; i < 8; i++ {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 || d.Client.OrderDelivery.Query().CountX(ctx) != 3 {
		t.Fatalf("duplicate delivery: %d", successes)
	}
	got, err := r.FetchDelivery(ctx, o.OrderNo, "", "127.0.0.1")
	if err != nil || got.Status != "completed" || len(got.Items) != 3 {
		t.Fatalf("multi-item delivery: %#v %v", got, err)
	}
}
