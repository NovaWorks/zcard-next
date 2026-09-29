package fulfillment

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"testing"
)

func TestPhysicalPackagesAndReturnedStock(t *testing.T) {
	d, _, repo := newFulfillData(t)
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7})
	s := NewAdminFulfillmentService(repo, d)
	p := d.Client.Product.Create().SetName("Parcel").SetSlug("parcel").SetPrice(500).SetGoodsType("physical").SetPhysicalStock(0).SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("SHIP-TEST").SetStatus("paid").SetCommerceVersion(1).SetShippingStatus("pending").SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(2).SetUnitPrice(500).SetAmount(1000).SetGoodsType("physical").SetFulfillmentType("shipping").SetFulfillmentStatus("pending").SaveX(ctx)
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
