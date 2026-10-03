package fulfillment

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
)

func TestPhysicalReturnHasIndependentReceiptAndInventorySnapshot(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		t.Run(fmt.Sprintf("tracked=%t", tracked), func(t *testing.T) {
			d, _, repo := newFulfillData(t)
			ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7})
			service := NewAdminFulfillmentService(repo, d)
			p := d.Client.Product.Create().SetName("Parcel").SetSlug("snapshot-return").SetPrice(500).SetGoodsType("physical").SetPhysicalStock(4).SetTrackInventory(!tracked).SaveX(ctx)
			o := d.Client.Order.Create().SetOrderNo("SNAPSHOT-RETURN").SetStatus("completed").SetCommerceVersion(1).SaveX(ctx)
			item := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetProductName("Parcel").SetQuantity(2).SetShippedQuantity(2).SetReceivedQuantity(2).SetUnitPrice(500).SetAmount(1000).SetGoodsType("physical").SetInventoryTracked(tracked).SetFulfillmentType("shipping").SaveX(ctx)
			reason := "验收完好"
			if !tracked {
				reason = strings.Repeat("完", 120)
			}
			req := &adminv1.RestockReturnRequest{OrderNo: o.OrderNo, ItemId: item.ID, Quantity: 1, Reason: reason, RequestKey: "snapshot-return-key"}
			for i := 0; i < 2; i++ {
				if _, err := service.RestockReturn(ctx, req); err != nil {
					t.Fatal(err)
				}
			}
			wantStock, wantMovements, event := int64(4), 0, "return_registered"
			if tracked {
				wantStock, wantMovements, event = 5, 1, "return_restocked"
			}
			if d.Client.Product.GetX(ctx, p.ID).PhysicalStock != wantStock || d.Client.PhysicalStockMovement.Query().CountX(ctx) != wantMovements || d.Client.OrderItem.GetX(ctx, item.ID).ReturnedQuantity != 1 || d.Client.PhysicalReturnReceipt.Query().CountX(ctx) != 1 || d.Client.OrderStatusEvent.Query().Where(orderstatusevent.Event(event)).CountX(ctx) != 1 {
				t.Fatal("return retry duplicated receipt, quantity, event or stock")
			}
			if receipt := d.Client.PhysicalReturnReceipt.Query().OnlyX(ctx); receipt.InventoryTracked != tracked || receipt.Reason != reason {
				t.Fatal("return receipt lost inventory snapshot or Unicode reason")
			}
			req.Reason = "changed receipt"
			if _, err := service.RestockReturn(ctx, req); err == nil {
				t.Fatal("return request accepted changed parameters")
			}
		})
	}
}

func TestPhysicalReturnPreservesLegacyMovementReplay(t *testing.T) {
	d, _, repo := newFulfillData(t)
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7})
	service := NewAdminFulfillmentService(repo, d)
	p := d.Client.Product.Create().SetName("Parcel").SetSlug("legacy-return").SetPrice(500).SetGoodsType("physical").SetPhysicalStock(1).SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("LEGACY-RETURN").SetStatus("completed").SetCommerceVersion(1).SaveX(ctx)
	item := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetShippedQuantity(1).SetReturnedQuantity(1).SetUnitPrice(500).SetAmount(500).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	req := &adminv1.RestockReturnRequest{OrderNo: o.OrderNo, ItemId: item.ID, Quantity: 1, Reason: "验收完好", RequestKey: "legacy-return-key"}
	key := fmt.Sprintf("return:%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%s", o.ID, req.RequestKey))))
	d.Client.PhysicalStockMovement.Create().SetProductID(p.ID).SetOrderID(o.ID).SetDelta(1).SetReference(key).SetReason("退货验收入库").SaveX(ctx)
	if _, err := service.RestockReturn(ctx, req); err != nil {
		t.Fatal("legacy completed return could not replay", err)
	}
	if d.Client.OrderItem.GetX(ctx, item.ID).ReturnedQuantity != 1 || d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 1 || d.Client.PhysicalReturnReceipt.Query().CountX(ctx) != 0 {
		t.Fatal("legacy return replay created another receipt or inventory movement")
	}
}
