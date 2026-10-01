package fulfillment

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPhysicalPackagesAndReturnedStock(t *testing.T) {
	d, _, repo := newFulfillData(t)
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7})
	s := NewAdminFulfillmentService(repo, d)
	p := d.Client.Product.Create().SetName("Parcel").SetSlug("parcel").SetPrice(500).SetGoodsType("physical").SetPhysicalStock(0).SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("SHIP-TEST").SetStatus("paid").SetCommerceVersion(1).SetShippingStatus("pending").SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetProductName("Parcel").SetSkuName("Blue").SetQuantity(2).SetUnitPrice(500).SetAmount(1000).SetGoodsType("physical").SetFulfillmentType("shipping").SetFulfillmentStatus("pending").SaveX(ctx)
	req := &adminv1.ShipOrderRequest{OrderNo: o.OrderNo, ItemIds: []uint64{it.ID}, Carrier: "Test Express", TrackingNo: "TEST001", RequestKey: "ship-request-1"}
	if _, e := s.ShipOrder(tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 99}), req); e == nil {
		t.Fatal("cross tenant shipping")
	}
	if _, e := s.ShipOrder(ctx, req); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ShipOrder(ctx, req); e != nil {
		t.Fatal(e)
	}
	if d.Client.Shipment.Query().CountX(ctx) != 1 || d.Client.Order.GetX(ctx, o.ID).Status != "delivered" {
		t.Fatal("shipment duplicate or completed too early")
	}
	pkg := d.Client.Shipment.Query().OnlyX(ctx)
	if e := data.ReceivePhysicalShipment(ctx, d, o.OrderNo, pkg.ID, "user", 1); e != nil {
		t.Fatal(e)
	}
	if e := data.ReceivePhysicalShipment(ctx, d, o.OrderNo, pkg.ID, "user", 1); e != nil {
		t.Fatal(e)
	}
	if d.Client.Order.GetX(ctx, o.ID).Status != "completed" || d.Client.OrderItem.GetX(ctx, it.ID).ReceivedQuantity != 2 {
		t.Fatal("receipt aggregation")
	}
	ret := &adminv1.RestockReturnRequest{OrderNo: o.OrderNo, ItemId: it.ID, Quantity: 1, Reason: "验收完好", RequestKey: "return-key-1"}
	if _, e := s.RestockReturn(ctx, ret); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RestockReturn(ctx, ret); e != nil {
		t.Fatal(e)
	}
	ret.Quantity = 2
	if _, e := s.RestockReturn(ctx, ret); e == nil {
		t.Fatal("changed return retry accepted")
	}
	if d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 1 {
		t.Fatal("return inventory duplicated")
	}
	events := d.Client.OrderStatusEvent.Query().Where(orderstatusevent.OrderID(o.ID), orderstatusevent.Event("return_restocked")).AllX(ctx)
	if len(events) != 1 || !strings.Contains(events[0].Reason, "Parcel（Blue）") || !strings.Contains(events[0].Reason, "入库 1 件") || !strings.Contains(events[0].Reason, "验收完好") {
		t.Fatalf("return record missing item, SKU, quantity or reason: %#v", events)
	}
}

func TestPendingDeliveriesIncludePhysicalRemaindersAndOrderPagination(t *testing.T) {
	d, _, repo := newFulfillData(t)
	ctx := context.Background()
	s := NewAdminFulfillmentService(repo, d)
	p := d.Client.Product.Create().SetName("Physical product").SetSlug("pending-physical").SetPrice(500).SaveX(ctx)
	manual := d.Client.Order.Create().SetOrderNo("PENDING-MANUAL").SetStatus("paid").SaveX(ctx)
	d.Client.OrderItem.Create().SetOrderID(manual.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetFulfillmentType("manual").SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("PENDING-PHYSICAL").SetStatus("partially_delivered").SetCommerceVersion(1).SaveX(ctx)
	remaining := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetProductName("Snapshot product").SetSkuName("Blue").SetQuantity(5).SetCanceledQuantity(2).SetShippedQuantity(1).SetUnitPrice(500).SetAmount(2500).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	second := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetSkuName("Red").SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(2).SetCanceledQuantity(2).SetUnitPrice(500).SetAmount(1000).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	shipped := d.Client.Order.Create().SetOrderNo("ALREADY-SHIPPED").SetStatus("fulfilling").SaveX(ctx)
	d.Client.OrderItem.Create().SetOrderID(shipped.ID).SetProductID(p.ID).SetQuantity(3).SetCanceledQuantity(1).SetShippedQuantity(2).SetUnitPrice(500).SetAmount(1500).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	d.Client.OrderItem.Create().SetOrderID(shipped.ID).SetProductID(p.ID).SetQuantity(1).SetCanceledQuantity(1).SetUnitPrice(500).SetAmount(500).SetFulfillmentType("manual").SaveX(ctx)
	foreign := d.Client.Order.Create().SetOrderNo("FOREIGN-PENDING").SetSubsiteID(99).SetStatus("paid").SaveX(ctx)
	d.Client.OrderItem.Create().SetOrderID(foreign.ID).SetProductID(p.ID).SetSubsiteID(99).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	first, err := s.ListPending(ctx, &adminv1.ListPendingRequest{Page: 1, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || len(first.Orders) != 2 {
		t.Fatalf("one order with two items must not distort pagination: %#v", first)
	}
	items := map[uint64]*adminv1.PendingOrder{}
	for _, row := range first.Orders {
		if row.OrderNo != o.OrderNo || row.GoodsType != "physical" || row.FulfillmentType != "shipping" {
			t.Fatalf("physical delivery route missing: %#v", row)
		}
		items[row.OrderItemId] = row
	}
	if items[remaining.ID].Quantity != 2 || items[remaining.ID].SkuName != "Blue" || items[remaining.ID].ProductName != "Snapshot product" || items[second.ID].Quantity != 1 {
		t.Fatalf("wrong pending quantity or product snapshot: %#v", first)
	}
	last, err := s.ListPending(ctx, &adminv1.ListPendingRequest{Page: 2, PageSize: 1})
	if err != nil || last.HasMore || len(last.Orders) != 1 || last.Orders[0].OrderNo != manual.OrderNo || last.Orders[0].FulfillmentType != "manual" {
		t.Fatalf("last page must preserve manual delivery and disable next: %#v %v", last, err)
	}
	d.Client.OrderItem.UpdateOneID(remaining.ID).SetShippedQuantity(3).ExecX(ctx)
	d.Client.OrderItem.UpdateOneID(second.ID).SetShippedQuantity(1).ExecX(ctx)
	after, err := s.ListPending(ctx, &adminv1.ListPendingRequest{Page: 1, PageSize: 20})
	if err != nil || after.HasMore || len(after.Orders) != 1 || after.Orders[0].OrderNo != manual.OrderNo {
		t.Fatalf("shipped/canceled items remain in pending list: %#v %v", after, err)
	}
	foreignPage, err := s.ListPending(tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 99}), &adminv1.ListPendingRequest{Page: 1, PageSize: 20})
	if err != nil || foreignPage.HasMore || len(foreignPage.Orders) != 1 || foreignPage.Orders[0].OrderNo != foreign.OrderNo {
		t.Fatalf("pending list must match current shipping tenant: %#v %v", foreignPage, err)
	}
}

func TestPhysicalReturnWithLongProductNameKeepsEssentialRecord(t *testing.T) {
	for _, name := range []string{strings.Repeat("实", 80), strings.Repeat("Parcel", 100)} {
		t.Run(name[:6], func(t *testing.T) {
			d, _, repo := newFulfillData(t)
			ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7})
			s := NewAdminFulfillmentService(repo, d)
			p := d.Client.Product.Create().SetName(name).SetSlug("return-long-name").SetPrice(0).SetGoodsType("physical").SaveX(ctx)
			o := d.Client.Order.Create().SetOrderNo("RETURN-LONG-NAME").SetStatus("completed").SetCommerceVersion(1).SaveX(ctx)
			it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetProductName(name).SetSkuName("蓝色 / M").SetQuantity(1).SetShippedQuantity(1).SetReceivedQuantity(1).SetUnitPrice(0).SetAmount(0).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
			if _, err := s.RestockReturn(ctx, &adminv1.RestockReturnRequest{OrderNo: o.OrderNo, ItemId: it.ID, Quantity: 1, Reason: "验收完好", RequestKey: "long-product-return"}); err != nil {
				t.Fatal("valid long product name blocked return", err)
			}
			event := d.Client.OrderStatusEvent.Query().Where(orderstatusevent.Event("return_restocked")).OnlyX(ctx)
			if !utf8.ValidString(event.Reason) || len(event.Reason) > 255 || !strings.Contains(event.Reason, "商品项 #") || !strings.Contains(event.Reason, "入库 1 件：验收完好") || d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 1 {
				t.Fatalf("return audit lost essential information: %q", event.Reason)
			}
		})
	}
}

func TestPhysicalMixedDeliveryProgress(t *testing.T) {
	d, _, _ := newFulfillData(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("Mixed").SetSlug("mixed").SetPrice(500).SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("MIXED-PROGRESS").SetStatus("paid").SetCommerceVersion(1).SaveX(ctx)
	physical := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetGoodsType("physical").SetFulfillmentType("shipping").SetShippedQuantity(1).SaveX(ctx)
	digital := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetGoodsType("virtual").SetFulfillmentType("manual").SaveX(ctx)
	check := func(want string) {
		t.Helper()
		current := d.Client.Order.GetX(ctx, o.ID)
		if e := data.RefreshPhysicalProgress(ctx, d, current); e != nil {
			t.Fatal(e)
		}
		if got := d.Client.Order.GetX(ctx, o.ID).Status; string(got) != want {
			t.Fatalf("progress %s want %s", got, want)
		}
	}
	check("partially_delivered")
	d.Client.OrderItem.UpdateOneID(digital.ID).SetFulfillmentStatus("delivered").ExecX(ctx)
	check("delivered")
	d.Client.OrderItem.UpdateOneID(physical.ID).SetReceivedQuantity(1).ExecX(ctx)
	check("completed")
}

func TestPhysicalOrderDeliveryOwnerAndTenantAccess(t *testing.T) {
	d, cipher, r := newFulfillData(t)
	ctx := context.Background()
	_, o := seedPaidOrderWithCards(t, d, cipher, "status", 1)
	d.Client.Order.UpdateOneID(o.ID).SetCommerceVersion(1).ExecX(ctx)
	owner := identity.WithClaims(ctx, &authn.Claims{Subject: o.UserID})
	if _, e := r.FetchDelivery(owner, o.OrderNo, "", ""); e != nil {
		t.Fatal("owner cannot retrieve mixed-order content", e)
	}
	other := identity.WithClaims(ctx, &authn.Claims{Subject: o.UserID + 1})
	if _, e := r.FetchDelivery(other, o.OrderNo, "", ""); e == nil {
		t.Fatal("non-owner read order")
	}
	if _, e := r.FetchDelivery(tenancy.WithContext(owner, tenancy.Context{SubsiteID: 99}), o.OrderNo, "", ""); e == nil {
		t.Fatal("cross-tenant owner read order")
	}
	if _, e := r.FetchDelivery(ctx, o.OrderNo, "", ""); e == nil {
		t.Fatal("guest without password read physical order")
	}
}

func TestPhysicalCanceledItemRejectsLateUpstreamDelivery(t *testing.T) {
	d, cipher, r := newFulfillData(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("Canceled upstream").SetSlug("canceled-upstream").SetPrice(1000).SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("LATE-UPSTREAM").SetStatus("paid").SetCommerceVersion(1).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(1000).SetAmount(1000).SetFulfillmentType("upstream").SetCanceledQuantity(1).SetFulfillmentStatus("refunded").SaveX(ctx)
	sealed, err := cipher.Seal("LATE-CARD", p.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AttachUpstreamDelivery(ctx, o.ID, it.ID, p.ID, []port.UpstreamDeliveryItem{{SealedContent: sealed, ContentHash: cipher.ContentHash("LATE-CARD")}}); err == nil {
		t.Fatal("canceled item received a late delivery")
	}
	if d.Client.Card.Query().CountX(ctx) != 0 || d.Client.OrderDelivery.Query().CountX(ctx) != 0 {
		t.Fatal("late callback left deliverable content")
	}
	if d.Client.Order.GetX(ctx, o.ID).Version != o.Version {
		t.Fatal("failed delivery did not roll back")
	}
}

func TestReviewCanceledManualDelivery(t *testing.T) {
	d, _, r := newFulfillData(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("Review").SetSlug("review").SetPrice(500).SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("REVIEW-CANCELED").SetStatus("fulfilling").SetCommerceVersion(1).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetGoodsType("virtual").SetFulfillmentType("manual").SetCanceledQuantity(1).SetFulfillmentStatus("refunded").SaveX(ctx)
	d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetGoodsType("physical").SetFulfillmentType("shipping").SetFulfillmentStatus("pending").SaveX(ctx)
	err := r.ManualDeliver(ctx, o.OrderNo, "REVIEW-SECRET", "", "", 7, it.ID)
	if err == nil {
		t.Fatalf("canceled item accepted manual delivery: deliveries=%d cards=%d", d.Client.OrderDelivery.Query().CountX(ctx), d.Client.Card.Query().CountX(ctx))
	}
}

func TestReviewNullSkuManualRelease(t *testing.T) {
	d, cipher, r := newFulfillData(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("Review null").SetSlug("review-null").SetPrice(500).SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("REVIEW-NULL").SetStatus("fulfilling").SetCommerceVersion(1).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetGoodsType("virtual").SetFulfillmentType("manual").SaveX(ctx)
	sealed, err := cipher.Seal("RESERVED", p.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := d.Client.Card.Create().SetProductID(p.ID).SetContent(sealed).SetContentHash(cipher.ContentHash("RESERVED")).SetStatus("reserved").SetOrderID(o.ID).SaveX(ctx)
	if err := r.ManualDeliver(ctx, o.OrderNo, "REPLACEMENT", "", "", 7, it.ID); err != nil {
		t.Fatal(err)
	}
	current := d.Client.Card.GetX(ctx, c.ID)
	if current.Status != "available" {
		t.Fatalf("replaced null-SKU card remained %s in order %d", current.Status, current.OrderID)
	}
}

func TestReviewTrackingCorrectionAfterFullRefund(t *testing.T) {
	d, _, r := newFulfillData(t)
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7})
	s := NewAdminFulfillmentService(r, d)
	o := d.Client.Order.Create().SetOrderNo("REFUNDED-SHIPMENT").SetCommerceVersion(1).SetStatus("refunded").SetShippingStatus("shipped").SaveX(ctx)
	pkg := d.Client.Shipment.Create().SetOrderID(o.ID).SetCarrier("Express").SetTrackingNo("WRONG001").SetItems(map[string]int32{}).SetAddress(map[string]string{}).SetAdminID(7).SetRequestKey("tracking-review-key").SaveX(ctx)
	_, err := s.UpdateShipping(ctx, &adminv1.UpdateShippingRequest{OrderNo: o.OrderNo, ShipmentId: pkg.ID, Carrier: "Express", TrackingNo: "RIGHT001", Reason: "更正已寄出包裹单号"})
	if err != nil {
		t.Fatalf("existing shipped parcel cannot correct tracking after refund: %v", err)
	}
	if d.Client.Shipment.GetX(ctx, pkg.ID).TrackingNo != "RIGHT001" || d.Client.Order.GetX(ctx, o.ID).Status != "refunded" {
		t.Fatal("tracking correction changed the refund state or was not persisted")
	}
	if _, err := s.ShipOrder(ctx, &adminv1.ShipOrderRequest{OrderNo: o.OrderNo, Carrier: "Express", TrackingNo: "NEW001", ItemIds: []uint64{1}, RequestKey: "refunded-new-shipment"}); err == nil {
		t.Fatal("refunded order accepted a new shipment")
	}
	if _, err := s.UpdateShipping(ctx, &adminv1.UpdateShippingRequest{OrderNo: o.OrderNo, Reason: "修改地址"}); err == nil {
		t.Fatal("refunded order accepted an address change")
	}
}
