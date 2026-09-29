//go:build integration

package testint

import (
	"context"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	entorder "github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPhysicalMySQL(t *testing.T) { physicalCommerce(MySQL(t)) }
func TestPhysicalPG(t *testing.T)    { physicalCommerce(PG(t)) }
func physicalCommerce(h *Harness) {
	t := h.T
	ctx := context.Background()
	d := h.Data
	gen, e := id.NewGenerator(1)
	if e != nil {
		t.Fatal(e)
	}
	out := data.NewOutboxWriter(d)
	uc := &order.OrderUsecase{Data: d, Gen: gen, Inv: inventory.NewCardRepoImpl(d, nil), Outbox: out}
	p := d.Client.Product.Create().SetName("Last parcel").SetSlug("last-parcel").SetPrice(1000).SetGoodsType("physical").SetPhysicalStock(1).SetShippingCountries([]string{"US"}).SetStatus(1).SaveX(ctx)
	address := map[string]string{"country": "US", "region": "CA", "city": "San Francisco", "address": "123 Test Street", "name": "Buyer", "phone": "+14155550100", "postal_code": "94105"}
	inputs := []order.CreateOrderInput{}
	for i := 0; i < 2; i++ {
		in := order.CreateOrderInput{Items: []order.OrderItemInput{{ProductID: p.ID, Quantity: 1}}, UserID: uint64(i + 1), QueryPassword: "test1234", ShippingAddress: address, QuoteOnly: true, IdempotencyKey: fmt.Sprintf("concurrent-%d", i)}
		q, e := uc.CreateOrder(ctx, in)
		if e != nil {
			t.Fatal(e)
		}
		in.QuoteOnly = false
		in.QuoteKey = q.QuoteKey
		inputs = append(inputs, in)
	}
	var wg sync.WaitGroup
	var wins atomic.Int32
	start := make(chan struct{})
	for _, in := range inputs {
		wg.Add(1)
		go func(in order.CreateOrderInput) {
			defer wg.Done()
			<-start
			if _, e := uc.CreateOrder(ctx, in); e == nil {
				wins.Add(1)
			}
		}(in)
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 || d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 0 || d.Client.Order.Query().CountX(ctx) != 1 {
		t.Fatal("concurrent physical oversell or no winner")
	}
	o := d.Client.Order.Query().OnlyX(ctx)
	if e = uc.MarkPaid(ctx, o.OrderNo); e != nil {
		t.Fatal(e)
	}
	it := d.Client.OrderItem.Query().Where(orderitem.OrderID(o.ID)).OnlyX(ctx)
	actor := identity.WithClaims(ctx, &authn.Claims{Subject: 7})
	ship := fulfillment.NewAdminFulfillmentService(nil, d)
	refund := payment.NewPaymentRepoImpl(d, nil, nil, nil, nil, nil, out, nil, nil, nil)
	zero := int64(0)
	start = make(chan struct{})
	wins.Store(0)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		if _, e := ship.ShipOrder(actor, &adminv1.ShipOrderRequest{OrderNo: o.OrderNo, ItemIds: []uint64{it.ID}, Carrier: "Test", TrackingNo: "TEST001", RequestKey: "shipping-race-key"}); e == nil {
			wins.Add(1)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		if _, e := refund.RefundPhysical(actor, o.ID, 7, &adminv1.CreateRefundRequest{Channel: "gateway", ExternalConfirmed: true, ExternalReference: "TEST-REFUND", AmountCents: 1000, ExpectedRefundedCents: &zero, ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"amount_cents":1000,"cancel_quantity":1}]`, it.ID)}); e == nil {
			wins.Add(1)
		}
	}()
	close(start)
	wg.Wait()
	it = d.Client.OrderItem.GetX(ctx, it.ID)
	o = d.Client.Order.GetX(ctx, o.ID)
	if wins.Load() != 1 || it.ShippedQuantity+it.CanceledQuantity != 1 {
		t.Fatalf("shipping/refund race %+v", it)
	}
	if it.ShippedQuantity == 1 {
		if o.Status == entorder.StatusRefunded || d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 0 {
			t.Fatal("shipped item refunded or restocked")
		}
	} else if o.Status != entorder.StatusRefunded || d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 1 {
		t.Fatal("refund lost its stock or status")
	}
}
