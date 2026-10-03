package order

import (
	"context"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
)

func TestPhysicalUntrackedInventoryCheckoutAndCancellation(t *testing.T) {
	for _, withSKU := range []bool{false, true} {
		name := "product"
		if withSKU {
			name = "sku-expiry"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			d, uc := seedPhysical(t)
			d.Client.Product.UpdateOneID(1).SetTrackInventory(false).SetPhysicalStock(0).ExecX(ctx)
			in := physicalInput()
			var skuID uint64
			if withSKU {
				uc.Catalog = catalog.NewProductRepoImpl(d, nil)
				sku := d.Client.ProductSku.Create().SetProductID(1).SetName("No stock limit").SetSpecValues(map[string]string{}).SetPhysicalStock(0).SaveX(ctx)
				skuID = sku.ID
				in.Items[0].SkuID = skuID
			}
			p := d.Client.Product.GetX(ctx, 1)
			if stock, err := data.PhysicalAvailable(ctx, d.Client, p); err != nil || stock != -1 {
				t.Fatalf("untracked product stock = %d, %v", stock, err)
			}
			sku, err := data.DeliverySKU(ctx, d.Client, p, skuID)
			if err != nil {
				t.Fatal(err)
			}
			if stock, err := data.LocalSKUStock(ctx, d, p, sku); err != nil || stock != -1 {
				t.Fatalf("untracked SKU stock = %d, %v", stock, err)
			}
			in = quotePhysical(t, uc, in)
			result, err := uc.CreateOrder(ctx, in)
			if err != nil {
				t.Fatal("untracked product with zero stored stock rejected", err)
			}
			item := d.Client.OrderItem.Query().OnlyX(ctx)
			if item.InventoryTracked || d.Client.PhysicalStockMovement.Query().CountX(ctx) != 0 {
				t.Fatal("untracked checkout reserved stock or lost its snapshot")
			}
			// Reopening inventory management does not turn older untracked orders
			// into reservations that may later increase the reconciled quantity.
			d.Client.Product.UpdateOneID(1).SetTrackInventory(true).SetPhysicalStock(8).ExecX(ctx)
			if withSKU {
				d.Client.ProductSku.UpdateOneID(skuID).SetPhysicalStock(8).ExecX(ctx)
				o := d.Client.Order.Query().OnlyX(ctx)
				d.Client.Order.UpdateOneID(o.ID).SetExpiredAt(time.Now().UTC().Add(-time.Hour)).ExecX(ctx)
				err = uc.cancelOrder(ctx, result.OrderNo, "expired", "system", 0, true)
			} else {
				err = uc.CancelOrder(ctx, result.OrderNo, "cancel", "user", 3)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := uc.CancelOrder(ctx, result.OrderNo, "retry", "user", 3); err != nil {
				t.Fatal(err)
			}
			if d.Client.PhysicalStockMovement.Query().CountX(ctx) != 0 || d.Client.Product.GetX(ctx, 1).PhysicalStock != 8 {
				t.Fatal("untracked cancellation changed reconciled inventory")
			}
			if withSKU && d.Client.ProductSku.GetX(ctx, skuID).PhysicalStock != 8 {
				t.Fatal("untracked expiry changed reconciled SKU inventory")
			}
		})
	}
}

func TestPhysicalTrackedReservationSurvivesDisabledInventory(t *testing.T) {
	ctx := context.Background()
	d, uc := seedPhysical(t)
	in := quotePhysical(t, uc, physicalInput())
	result, err := uc.CreateOrder(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Client.OrderItem.Query().OnlyX(ctx).InventoryTracked {
		t.Fatal("tracked order snapshot missing")
	}
	d.Client.Product.UpdateOneID(1).SetTrackInventory(false).ExecX(ctx)
	if err := uc.CancelOrder(ctx, result.OrderNo, "cancel", "user", 3); err != nil {
		t.Fatal(err)
	}
	if d.Client.Product.GetX(ctx, 1).PhysicalStock != 5 || d.Client.PhysicalStockMovement.Query().CountX(ctx) != 2 {
		t.Fatal("disabled product lost an older tracked reservation")
	}
}
