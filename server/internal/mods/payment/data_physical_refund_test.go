package payment

import (
	"context"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	"testing"
)

func TestPhysicalRefundAllocationCancellationAndRetry(t *testing.T) {
	ctx := context.Background()
	d, r, _, _, _, _ := newCallbackEnv(t)
	r.outbox = data.NewOutboxWriter(d)
	p := d.Client.Product.Create().SetName("Parcel").SetSlug("parcel").SetPrice(500).SetGoodsType("physical").SetPhysicalStock(3).SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("PHYSICAL-REFUND").SetUserID(1).SetCommerceVersion(1).SetStatus("paid").SetTotalAmount(1300).SetShippingAmount(300).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(2).SetUnitPrice(500).SetAmount(1000).SetPaidAmount(1000).SetShippingAmount(300).SetGoodsType("physical").SetFulfillmentType("shipping").SetFulfillmentStatus("pending").SaveX(ctx)
	req := &adminv1.CreateRefundRequest{Channel: "wallet", AmountCents: 500, ExpectedRefundedCents: refundPtr(0), ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"amount_cents":500,"cancel_quantity":1}]`, it.ID)}
	if _, e := r.RefundPhysical(ctx, o.ID, 7, req); e != nil {
		t.Fatal(e)
	}
	if _, e := r.RefundPhysical(ctx, o.ID, 7, req); e == nil {
		t.Fatal("duplicate accepted")
	}
	if d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 4 || d.Client.WalletAccount.Query().OnlyX(ctx).Available != 500 {
		t.Fatal("refund stock or wallet incorrect")
	}
	d.Client.OrderItem.UpdateOneID(it.ID).SetShippedQuantity(1).SetFulfillmentStatus("shipped").ExecX(ctx)
	req.AmountCents = 800
	req.ExpectedRefundedCents = refundPtr(500)
	req.ItemAllocationsJson = fmt.Sprintf(`[{"item_id":%d,"amount_cents":500,"shipping_cents":300,"cancel_quantity":1}]`, it.ID)
	if _, e := r.RefundPhysical(ctx, o.ID, 7, req); e == nil {
		t.Fatal("shipped cancellation accepted")
	}
	req.ItemAllocationsJson = fmt.Sprintf(`[{"item_id":%d,"amount_cents":500,"shipping_cents":300}]`, it.ID)
	if _, e := r.RefundPhysical(ctx, o.ID, 7, req); e != nil {
		t.Fatal(e)
	}
	if d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 4 || d.Client.WalletAccount.Query().OnlyX(ctx).Available != 1300 || d.Client.OutboxEvent.Query().CountX(ctx) != 2 {
		t.Fatal("multi refund or restock incorrect")
	}
	if _, e := r.RefundToWallet(ctx, o.ID, 1, refundPtr(1300), "", 7); e == nil {
		t.Fatal("legacy refund bypass accepted")
	}
}

func TestPhysicalGuestCancellationWithoutMoney(t *testing.T) {
	ctx := context.Background()
	d, r, _, _, _, _ := newCallbackEnv(t)
	r.outbox = data.NewOutboxWriter(d)
	p := d.Client.Product.Create().SetName("Gift").SetSlug("gift").SetPrice(0).SetGoodsType("physical").SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("GUEST-GIFT").SetCommerceVersion(1).SetStatus("paid").SetTotalAmount(0).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(0).SetAmount(0).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	req := &adminv1.CreateRefundRequest{Channel: "gateway", RequestKey: "gift-cancel-request", ExpectedRefundedCents: refundPtr(0), ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"cancel_quantity":1}]`, it.ID)}
	if _, e := r.RefundPhysical(ctx, o.ID, 7, req); e != nil {
		t.Fatal(e)
	}
	if d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 1 || d.Client.Order.GetX(ctx, o.ID).ShippingStatus != "canceled" {
		t.Fatal("zero cancellation failed")
	}
	if _, e := r.RefundPhysical(ctx, o.ID, 7, req); e != nil {
		t.Fatal("idempotent cancellation replay", e)
	}
	if d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 1 {
		t.Fatal("duplicate cancellation restored stock twice")
	}
}

func TestPhysicalWalletRefundKeyedReplay(t *testing.T) {
	ctx := context.Background()
	d, r, _, _, _, _ := newCallbackEnv(t)
	r.outbox = data.NewOutboxWriter(d)
	p := d.Client.Product.Create().SetName("Parcel").SetSlug("wallet-retry").SetPrice(500).SetGoodsType("physical").SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("WALLET-KEYED-RETRY").SetUserID(1).SetCommerceVersion(1).SetStatus("paid").SetTotalAmount(500).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetPaidAmount(500).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	req := &adminv1.CreateRefundRequest{Channel: "wallet", RequestKey: "wallet-refund-retry", AmountCents: 500, ExpectedRefundedCents: refundPtr(0), ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"amount_cents":500,"cancel_quantity":1}]`, it.ID)}
	first, err := r.RefundPhysical(ctx, o.ID, 7, req)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := r.RefundPhysical(ctx, o.ID, 7, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != replayed.ID || d.Client.RefundOrder.Query().CountX(ctx) != 1 || d.Client.WalletAccount.Query().OnlyX(ctx).Available != 500 || d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 1 || d.Client.OutboxEvent.Query().CountX(ctx) != 1 {
		t.Fatal("replayed refund duplicated wallet credit, inventory, receipt or event")
	}
	if d.Client.Order.GetX(ctx, o.ID).Status != "refunded" {
		t.Fatal("full refund state lost on replay")
	}
}

func TestReviewFullCancellationDoesNotEmitDelivery(t *testing.T) {
	ctx := context.Background()
	d, r, _, _, _, _ := newCallbackEnv(t)
	r.outbox = data.NewOutboxWriter(d)
	p := d.Client.Product.Create().SetName("Parcel").SetSlug("review").SetPrice(500).SetGoodsType("physical").SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("REVIEW-CANCEL").SetUserID(1).SetCommerceVersion(1).SetStatus("paid").SetTotalAmount(500).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetPaidAmount(500).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	_, err := r.RefundPhysical(ctx, o.ID, 7, &adminv1.CreateRefundRequest{Channel: "wallet", AmountCents: 500, ExpectedRefundedCents: refundPtr(0), ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"amount_cents":500,"cancel_quantity":1}]`, it.ID)})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range d.Client.OutboxEvent.Query().AllX(ctx) {
		if event.Type == "order.delivered" {
			t.Errorf("UNEXPECTED delivery event on full cancellation; final order=%s", d.Client.Order.GetX(ctx, o.ID).Status)
		}
	}
}
func TestReviewCanceledVirtualCannotStillDeliver(t *testing.T) {
	ctx := context.Background()
	d, r, _, _, _, _ := newCallbackEnv(t)
	r.outbox = data.NewOutboxWriter(d)
	p := d.Client.Product.Create().SetName("Virtual").SetSlug("review-card").SetPrice(500).SaveX(ctx)
	pp := d.Client.Product.Create().SetName("Parcel").SetSlug("review-parcel").SetPrice(500).SetGoodsType("physical").SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("REVIEW-MIXED").SetUserID(1).SetCommerceVersion(1).SetStatus("paid").SetTotalAmount(1000).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetPaidAmount(500).SetFulfillmentType("auto").SaveX(ctx)
	d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(pp.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetPaidAmount(500).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	card := d.Client.Card.Create().SetProductID(p.ID).SetContent([]byte{1}).SetContentHash("review-card").SetStatus("reserved").SetOrderID(o.ID).SaveX(ctx)
	_, err := r.RefundPhysical(ctx, o.ID, 7, &adminv1.CreateRefundRequest{Channel: "wallet", AmountCents: 500, ExpectedRefundedCents: refundPtr(0), ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"amount_cents":500,"cancel_quantity":1}]`, it.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Client.Card.GetX(ctx, card.ID).Status; got != "available" {
		t.Errorf("canceled card status=%s, want available", got)
	}
	if err := fulfillment.NewDeliveryRepoImpl(d, nil, nil, nil).FulfillOrder(ctx, o.OrderNo); err != nil {
		t.Fatal(err)
	}
	if n := d.Client.OrderDelivery.Query().CountX(ctx); n != 0 {
		t.Errorf("UNEXPECTED %d delivery after virtual item refunded/canceled", n)
	}
}
func TestReviewExternalReceiptCannotBeCountedTwice(t *testing.T) {
	ctx := context.Background()
	d, r, _, _, _, _ := newCallbackEnv(t)
	r.outbox = data.NewOutboxWriter(d)
	p := d.Client.Product.Create().SetName("Parcel").SetSlug("review").SetPrice(1000).SetGoodsType("physical").SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("REVIEW-RECEIPT").SetCommerceVersion(1).SetStatus("delivered").SetTotalAmount(1000).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(1000).SetAmount(1000).SetPaidAmount(1000).SetGoodsType("physical").SetFulfillmentType("shipping").SetShippedQuantity(1).SaveX(ctx)
	req := &adminv1.CreateRefundRequest{Channel: "gateway", AmountCents: 200, ExpectedRefundedCents: refundPtr(0), ExternalConfirmed: true, ExternalReference: "same-receipt-001", ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"amount_cents":200}]`, it.ID)}
	if _, e := r.RefundPhysical(ctx, o.ID, 7, req); e != nil {
		t.Fatal(e)
	}
	req.ExpectedRefundedCents = refundPtr(200)
	if _, e := r.RefundPhysical(ctx, o.ID, 7, req); e == nil {
		t.Fatalf("same external receipt counted twice; item refunded=%d", d.Client.OrderItem.GetX(ctx, it.ID).RefundedAmount)
	}
}

func TestPhysicalFullRefundRequiresVirtualCancellation(t *testing.T) {
	ctx := context.Background()
	d, r, _, _, _, _ := newCallbackEnv(t)
	r.outbox = data.NewOutboxWriter(d)
	p := d.Client.Product.Create().SetName("Mixed").SetSlug("full-mixed").SetPrice(500).SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("FULL-MIXED").SetUserID(1).SetCommerceVersion(1).SetStatus("paid").SetTotalAmount(1000).SaveX(ctx)
	physical := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetPaidAmount(500).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	virtual := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetPaidAmount(500).SetFulfillmentType("manual").SaveX(ctx)
	req := &adminv1.CreateRefundRequest{Channel: "wallet", AmountCents: 1000, ExpectedRefundedCents: refundPtr(0), ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"amount_cents":500,"cancel_quantity":1},{"item_id":%d,"amount_cents":500}]`, physical.ID, virtual.ID)}
	if _, e := r.RefundPhysical(ctx, o.ID, 7, req); e == nil {
		t.Fatal("pending virtual obligation left behind")
	}
	if d.Client.RefundOrder.Query().CountX(ctx) != 0 || d.Client.Order.GetX(ctx, o.ID).Status != "paid" {
		t.Fatal("rejected refund mutated order")
	}
}

func TestPhysicalRefundBlocksAmbiguousProcurement(t *testing.T) {
	for _, state := range []string{"pending", "submitted", "polling", "manual", "rejected"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			d, r, _, _, _, _ := newCallbackEnv(t)
			r.outbox = data.NewOutboxWriter(d)
			p := d.Client.Product.Create().SetName("Upstream").SetSlug("upstream").SetPrice(500).SaveX(ctx)
			o := d.Client.Order.Create().SetOrderNo("REFUND-PURCHASE").SetUserID(1).SetCommerceVersion(1).SetStatus("paid").SetTotalAmount(500).SaveX(ctx)
			it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetPaidAmount(500).SetFulfillmentType("upstream").SaveX(ctx)
			d.Client.ProcurementOrder.Create().SetOrderItemID(it.ID).SetConnectionID(1).SetDedupeKey("test-purchase").SetStatus(procurementorder.Status(state)).SaveX(ctx)
			req := &adminv1.CreateRefundRequest{Channel: "wallet", AmountCents: 500, ExpectedRefundedCents: refundPtr(0), ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"amount_cents":500,"cancel_quantity":1}]`, it.ID)}
			_, e := r.RefundPhysical(ctx, o.ID, 7, req)
			if state == "rejected" {
				if e != nil {
					t.Fatal(e)
				}
			} else {
				if e == nil {
					t.Fatal("ambiguous purchase refunded")
				}
				if d.Client.RefundOrder.Query().CountX(ctx) != 0 {
					t.Fatal("refund leaked")
				}
			}
		})
	}
}

func TestPhysicalRefundHistoryIsScopedAndDetailed(t *testing.T) {
	ctx := context.Background()
	d, r, _, _, _, _ := newCallbackEnv(t)
	own := d.Client.Order.Create().SetOrderNo("HISTORY-OWN").SaveX(ctx)
	other := d.Client.Order.Create().SetOrderNo("HISTORY-OTHER").SetSubsiteID(99).SaveX(ctx)
	rf := d.Client.RefundOrder.Create().SetOrderID(own.ID).SetAmount(500).SetShippingAmount(100).SetFeeAmount(10).SetOperatorID(7).SetItemAllocations([]map[string]int64{{"item_id": 1, "amount_cents": 400, "shipping_cents": 100, "cancel_quantity": 1}}).SetChannel("wallet").SetStatus("succeeded").SaveX(ctx)
	d.Client.RefundOrder.Create().SetOrderID(other.ID).SetAmount(900).SetChannel("wallet").SetStatus("succeeded").SaveX(ctx)
	rows, e := r.ListRefunds(ctx, "", &adminv1.ListRefundsRequest{OrderNo: own.OrderNo})
	if e != nil || len(rows) != 1 || rows[0].ID != rf.ID {
		t.Fatalf("history scope %v %v", rows, e)
	}
	pb := ToRefundPB(rows[0], own.OrderNo)
	if pb.ShippingCents != 100 || pb.ItemAllocationsJson == "" || pb.OperatorId != 7 || pb.CreatedAt <= 0 {
		t.Fatalf("missing refund details: %+v", pb)
	}
	rows, e = r.ListRefunds(ctx, "", &adminv1.ListRefundsRequest{OrderNo: other.OrderNo})
	if e != nil || len(rows) != 0 {
		t.Fatal("cross-tenant refund history exposed")
	}
	rows, e = r.ListRefunds(ctx, "", &adminv1.ListRefundsRequest{OrderNo: own.OrderNo, BeforeId: rf.ID})
	if e != nil || len(rows) != 0 {
		t.Fatal("refund history cursor duplicated receipt")
	}
}

func TestReviewPartialCancelRetryWithoutMoney(t *testing.T) {
	ctx := context.Background()
	d, r, _, _, _, _ := newCallbackEnv(t)
	r.outbox = data.NewOutboxWriter(d)
	p := d.Client.Product.Create().SetName("Review").SetSlug("cancel-retry").SetGoodsType("physical").SetPrice(500).SetPhysicalStock(0).SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("CANCEL-RETRY").SetCommerceVersion(1).SetStatus("paid").SetTotalAmount(2000).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(4).SetUnitPrice(500).SetAmount(2000).SetPaidAmount(2000).SetGoodsType("physical").SetFulfillmentType("shipping").SetFulfillmentStatus("pending").SaveX(ctx)
	req := &adminv1.CreateRefundRequest{Channel: "gateway", RequestKey: "partial-cancel-request", ExpectedRefundedCents: refundPtr(0), ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"cancel_quantity":1}]`, it.ID)}
	if _, err := r.RefundPhysical(ctx, o.ID, 7, req); err != nil {
		t.Fatal(err)
	}
	_, err := r.RefundPhysical(ctx, o.ID, 7, req)
	if err != nil {
		t.Fatal(err)
	}
	if d.Client.OrderItem.GetX(ctx, it.ID).CanceledQuantity != 1 || d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 1 || d.Client.RefundOrder.Query().CountX(ctx) != 1 {
		t.Fatal("retry mutated stock or cancellation")
	}
	req.ItemAllocationsJson = fmt.Sprintf(`[{"item_id":%d,"cancel_quantity":2}]`, it.ID)
	if _, err = r.RefundPhysical(ctx, o.ID, 7, req); err == nil {
		t.Fatal("changed retry accepted")
	}
	req.RequestKey = ""
	if _, err = r.RefundPhysical(ctx, o.ID, 7, req); err == nil {
		t.Fatal("unkeyed cancellation accepted")
	}
	req.RequestKey = "second-cancel-request"
	if _, err = r.RefundPhysical(ctx, o.ID, 7, req); err != nil {
		t.Fatal("new cancellation rejected", err)
	}
	if d.Client.OrderItem.GetX(ctx, it.ID).CanceledQuantity != 3 || d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 3 {
		t.Fatal("new cancellation totals")
	}

}
