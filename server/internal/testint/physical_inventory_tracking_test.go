//go:build integration

package testint

import (
	"context"
	"fmt"
	"sync"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/physicalreturnreceipt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/physicalstockmovement"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
)

func TestPhysicalInventoryModeMySQL(t *testing.T) { physicalInventoryMode(MySQL(t)) }
func TestPhysicalInventoryModePG(t *testing.T)    { physicalInventoryMode(PG(t)) }

func physicalInventoryMode(h *Harness) {
	t, d := h.T, h.Data
	ctx := context.Background()
	gen, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	repo := catalog.NewProductRepoImpl(d, nil)
	uc := &order.OrderUsecase{Data: d, Gen: gen, Catalog: repo, Inv: inventory.NewCardRepoImpl(d, nil), Outbox: data.NewOutboxWriter(d)}
	address := map[string]string{"country": "US", "region": "CA", "city": "San Francisco", "address": "123 Test Street", "name": "Buyer", "phone": "+14155550100", "postal_code": "94105"}
	for _, withSKU := range []bool{false, true} {
		t.Run(fmt.Sprintf("sku=%t", withSKU), func(t *testing.T) {
			baseStock := int64(6)
			if withSKU {
				baseStock = 0
			}
			p := d.Client.Product.Create().SetName("Inventory mode").SetSlug(fmt.Sprintf("inventory-mode-%t", withSKU)).SetProductProperty("physical").SetTrackInventory(false).SetPrice(1000).SetPhysicalStock(baseStock).SetShippingCountries([]string{"US"}).SetStatus(1).SaveX(ctx)
			var skuID uint64
			if withSKU {
				skuID = d.Client.ProductSku.Create().SetProductID(p.ID).SetName("Size").SetSpecValues(map[string]string{}).SetPhysicalStock(6).SaveX(ctx).ID
			}
			storedStock := func() int64 {
				if withSKU {
					return d.Client.ProductSku.GetX(ctx, skuID).PhysicalStock
				}
				return d.Client.Product.GetX(ctx, p.ID).PhysicalStock
			}
			quote := func(key string, quantity int32) order.CreateOrderInput {
				t.Helper()
				in := order.CreateOrderInput{Items: []order.OrderItemInput{{ProductID: p.ID, SkuID: skuID, Quantity: quantity}}, UserID: 1, ShippingAddress: address, QuoteOnly: true, IdempotencyKey: fmt.Sprintf("%t-%s", withSKU, key)}
				q, err := uc.CreateOrder(ctx, in)
				if err != nil {
					t.Fatal(err)
				}
				in.QuoteOnly, in.QuoteKey = false, q.QuoteKey
				return in
			}
			stale := quote("stale-mode", 1)
			inputs := []order.CreateOrderInput{quote("unlimited-a", 8), quote("unlimited-b", 8)}
			results := make([]*order.CreateOrderResult, len(inputs))
			errs := make([]error, len(inputs))
			concurrentCalls(len(inputs), func(i int) { results[i], errs[i] = uc.CreateOrder(ctx, inputs[i]) })
			for _, err := range errs {
				if err != nil {
					t.Fatal("untracked concurrent orders exceeding stored stock rejected", err)
				}
			}
			if storedStock() != 6 || d.Client.PhysicalStockMovement.Query().Where(physicalstockmovement.ProductID(p.ID)).CountX(ctx) != 0 {
				t.Fatal("untracked orders changed frozen inventory")
			}
			items := d.Client.OrderItem.Query().Where(orderitem.ProductID(p.ID)).AllX(ctx)
			if len(items) != 2 || items[0].InventoryTracked || items[1].InventoryTracked {
				t.Fatal("untracked checkout snapshot was lost")
			}
			on := true
			if !withSKU {
				if _, err := repo.UpdateProduct(ctx, p.ID, catalogport.ProductInput{TrackInventory: &on, Status: -1}); err == nil {
					t.Fatal("inventory reopened without explicit reconciliation")
				}
			}
			if _, err := repo.UpdateProduct(ctx, p.ID, catalogport.ProductInput{TrackInventory: &on, PhysicalStock: &baseStock, ExpectedPhysicalStock: &baseStock, Status: -1}); err != nil {
				t.Fatal(err)
			}
			if _, err := uc.CreateOrder(ctx, stale); err == nil {
				t.Fatal("old quote accepted after inventory mode changed")
			}
			concurrentCalls(len(results), func(i int) { errs[i] = uc.CancelOrder(ctx, results[i].OrderNo, "cancel untracked", "user", 1) })
			for _, err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			if storedStock() != 6 || d.Client.PhysicalStockMovement.Query().Where(physicalstockmovement.ProductID(p.ID)).CountX(ctx) != 0 {
				t.Fatal("canceling older untracked orders changed reconciled inventory")
			}
			tracked, err := uc.CreateOrder(ctx, quote("tracked-order", 2))
			if err != nil || storedStock() != 4 {
				t.Fatalf("tracked order did not reserve exactly two units: stock=%d err=%v", storedStock(), err)
			}
			off := false
			hiddenStock := int64(99)
			if _, err := repo.UpdateProduct(ctx, p.ID, catalogport.ProductInput{TrackInventory: &off, PhysicalStock: &hiddenStock, ExpectedPhysicalStock: &hiddenStock, Status: -1}); err != nil {
				t.Fatal(err)
			}
			if err := uc.CancelOrder(ctx, tracked.OrderNo, "cancel tracked", "user", 1); err != nil {
				t.Fatal(err)
			}
			if err := uc.CancelOrder(ctx, tracked.OrderNo, "retry tracked", "user", 1); err != nil {
				t.Fatal(err)
			}
			if storedStock() != 6 || d.Client.PhysicalStockMovement.Query().Where(physicalstockmovement.ProductID(p.ID)).CountX(ctx) != 2 {
				t.Fatal("disabled inventory discarded or duplicated an earlier tracked reservation")
			}
		})
	}
}

func TestPhysicalInventorySnapshotRetriesMySQL(t *testing.T) {
	physicalInventorySnapshotRetries(MySQL(t))
}
func TestPhysicalInventorySnapshotRetriesPG(t *testing.T) { physicalInventorySnapshotRetries(PG(t)) }

func physicalInventorySnapshotRetries(h *Harness) {
	t, d := h.T, h.Data
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7})
	returns := fulfillment.NewAdminFulfillmentService(nil, d)
	refunds := payment.NewPaymentRepoImpl(d, nil, nil, nil, nil, nil, data.NewOutboxWriter(d), nil, nil, nil)
	for _, tracked := range []bool{false, true} {
		t.Run(fmt.Sprintf("tracked-snapshot=%t", tracked), func(t *testing.T) {
			p := d.Client.Product.Create().SetName("Snapshot").SetSlug(fmt.Sprintf("snapshot-%t", tracked)).SetProductProperty("physical").SetTrackInventory(!tracked).SetPhysicalStock(4).SetPrice(500).SaveX(ctx)
			o := d.Client.Order.Create().SetOrderNo(fmt.Sprintf("SNAPSHOT-%t", tracked)).SetStatus("paid").SetCommerceVersion(1).SetTotalAmount(2000).SaveX(ctx)
			item := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetProductName("Snapshot").SetQuantity(4).SetShippedQuantity(2).SetUnitPrice(500).SetAmount(2000).SetPaidAmount(2000).SetGoodsType("physical").SetInventoryTracked(tracked).SetFulfillmentType("shipping").SaveX(ctx)
			zero := int64(0)
			refund := &adminv1.CreateRefundRequest{Channel: "gateway", RequestKey: "snapshot-refund-key", ExpectedRefundedCents: &zero, ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"cancel_quantity":1}]`, item.ID)}
			concurrentCalls(3, func(_ int) { _, _ = refunds.RefundPhysical(ctx, o.ID, 7, refund) })
			if _, err := refunds.RefundPhysical(ctx, o.ID, 7, refund); err != nil {
				t.Fatal("refund retry failed", err)
			}
			ret := &adminv1.RestockReturnRequest{OrderNo: o.OrderNo, ItemId: item.ID, Quantity: 1, Reason: "验收完好", RequestKey: "snapshot-return-key"}
			concurrentCalls(3, func(_ int) { _, _ = returns.RestockReturn(ctx, ret) })
			if _, err := returns.RestockReturn(ctx, ret); err != nil {
				t.Fatal("return retry failed", err)
			}
			wantStock, wantMovements, event := int64(4), 0, "return_registered"
			if tracked {
				wantStock, wantMovements, event = 6, 2, "return_restocked"
			}
			item = d.Client.OrderItem.GetX(ctx, item.ID)
			if item.CanceledQuantity != 1 || item.ReturnedQuantity != 1 || d.Client.Product.GetX(ctx, p.ID).PhysicalStock != wantStock || d.Client.PhysicalStockMovement.Query().Where(physicalstockmovement.ProductID(p.ID)).CountX(ctx) != wantMovements {
				t.Fatal("concurrent refund/return replay duplicated quantities or ignored inventory snapshot")
			}
			if d.Client.PhysicalReturnReceipt.Query().Where(physicalreturnreceipt.ItemID(item.ID)).CountX(ctx) != 1 || d.Client.OrderStatusEvent.Query().Where(orderstatusevent.OrderID(o.ID), orderstatusevent.Event(event)).CountX(ctx) != 1 {
				t.Fatal("return replay duplicated receipt or history event")
			}
			ret.Quantity = 2
			if _, err := returns.RestockReturn(ctx, ret); err == nil {
				t.Fatal("same return request accepted changed quantity")
			}
		})
	}
}

func concurrentCalls(n int, call func(int)) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; call(i) }(i)
	}
	close(start)
	wg.Wait()
}
