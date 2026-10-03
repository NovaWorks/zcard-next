package fulfillment

import (
	"context"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
)

func TestShippingAddressSecondLineCorrectionAndParcelSnapshot(t *testing.T) {
	d, _, repo := newFulfillData(t)
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7})
	service := NewAdminFulfillmentService(repo, d)
	p := d.Client.Product.Create().SetName("Parcel").SetSlug("address-lines").SetPrice(500).SetGoodsType("physical").SaveX(ctx)
	address := map[string]string{"country": "AU", "region": "NSW", "city": "Sydney", "district": "", "address": "3 Example Street", "address_line2": "  Unit 4  ", "name": "Buyer", "phone": "+61412345678", "postal_code": "2000"}
	o := d.Client.Order.Create().SetOrderNo("ADDRESS-LINES").SetStatus("paid").SetCommerceVersion(1).SetShippingStatus("pending").SetShippingAddress(map[string]string{"country": "AU"}).SaveX(ctx)
	item := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	if _, err := service.UpdateShipping(ctx, &adminv1.UpdateShippingRequest{OrderNo: o.OrderNo, Address: address, Reason: "补充单元号"}); err != nil {
		t.Fatal(err)
	}
	stored := d.Client.Order.GetX(ctx, o.ID).ShippingAddress
	if stored["address_line2"] != "Unit 4" || stored["address"] != "3 Example Street" || stored["district"] != "" {
		t.Fatalf("address correction lost distinct address lines: %#v", stored)
	}
	if _, err := service.ShipOrder(ctx, &adminv1.ShipOrderRequest{OrderNo: o.OrderNo, ItemIds: []uint64{item.ID}, Carrier: "Express", TrackingNo: "AU001", RequestKey: "address-line-shipment"}); err != nil {
		t.Fatal(err)
	}
	parcel := d.Client.Shipment.Query().OnlyX(ctx)
	if parcel.Address["address_line2"] != "Unit 4" || parcel.Address["address"] != "3 Example Street" {
		t.Fatal("parcel snapshot omitted the optional address line")
	}
	address["address_line2"] = "Unit 9"
	if _, err := service.UpdateShipping(ctx, &adminv1.UpdateShippingRequest{OrderNo: o.OrderNo, Address: address, Reason: "修改已寄出地址"}); err == nil {
		t.Fatal("shipped address snapshot was editable")
	}
	if d.Client.Shipment.GetX(ctx, parcel.ID).Address["address_line2"] != "Unit 4" {
		t.Fatal("shipped parcel address was changed")
	}
}
