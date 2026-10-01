package catalog

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"testing"
)

func TestPhysicalStockAdjustmentsAndDeliveryGuards(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()
	repo := NewProductRepoImpl(d, nil)
	p := d.Client.Product.Create().SetName("Parcel").SetSlug("parcel").SetPrice(100).SetGoodsType("physical").SetPhysicalStock(5).SetShippingCountries([]string{"US"}).SaveX(ctx)
	for _, target := range []int64{8, 3} {
		old := d.Client.Product.GetX(ctx, p.ID).PhysicalStock
		if _, e := repo.UpdateProduct(ctx, p.ID, port.ProductInput{PhysicalStock: &target, ExpectedPhysicalStock: &old}); e != nil {
			t.Fatal(e)
		}
		if got := d.Client.Product.GetX(ctx, p.ID).PhysicalStock; got != target {
			t.Fatalf("stock %d want %d", got, target)
		}
	}
	target, stale := int64(99), int64(8)
	if _, e := repo.UpdateProduct(ctx, p.ID, port.ProductInput{PhysicalStock: &target, ExpectedPhysicalStock: &stale}); e == nil {
		t.Fatal("stale stock overwrote inventory")
	}
	if _, e := svc.SetDeliverySource(ctx, &adminv1.SetDeliverySourceRequest{ProductId: p.ID, ExpectedRevision: d.Client.Product.GetX(ctx, p.ID).LockVersion}); e == nil {
		t.Fatal("physical product accepted digital delivery")
	}
	virtual := "virtual"
	if _, e := repo.UpdateProduct(ctx, p.ID, port.ProductInput{GoodsType: &virtual}); e == nil {
		t.Fatal("physical nature mutated")
	}
	qty := int64(2)
	if _, e := repo.CreateSku(ctx, SkuInput{ProductID: p.ID, Name: "Blocked", PhysicalStock: &qty}); e == nil {
		t.Fatal("base inventory hidden by SKU creation")
	}
	zero, oldBase := int64(0), int64(3)
	if _, e := repo.UpdateProduct(ctx, p.ID, port.ProductInput{PhysicalStock: &zero, ExpectedPhysicalStock: &oldBase}); e != nil {
		t.Fatal(e)
	}
	sku, e := repo.CreateSku(ctx, SkuInput{ProductID: p.ID, Name: "Large", SpecValues: map[string]string{}, PhysicalStock: &qty})
	if e != nil {
		t.Fatal(e)
	}
	for _, next := range []int64{4, 1} {
		old := d.Client.ProductSku.GetX(ctx, sku.ID).PhysicalStock
		if _, e = repo.UpdateSku(ctx, sku.ID, SkuInput{PhysicalStock: &next, ExpectedPhysicalStock: &old}); e != nil {
			t.Fatal(e)
		}
	}
	if d.Client.ProductSku.GetX(ctx, sku.ID).PhysicalStock != 1 {
		t.Fatal("SKU stock adjustment reused a ledger key")
	}
	o := d.Client.Order.Create().SetOrderNo("physical-delete-test").SetTotalAmount(100).SaveX(ctx)
	d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetSkuID(sku.ID).SetQuantity(1).SetUnitPrice(100).SetAmount(100).SetFulfillmentType("shipping").SaveX(ctx)
	if e = repo.DeleteSku(ctx, sku.ID); e == nil {
		t.Fatal("referenced physical SKU deleted")
	}
	if e = repo.DeleteProduct(ctx, p.ID); e != nil {
		t.Fatal("physical product archive failed", e)
	}
}

func TestPhysicalSkuTopologyPreservesInventory(t *testing.T) {
	d, _ := newStatsEnv(t)
	ctx := context.Background()
	r := NewProductRepoImpl(d, nil)
	p := d.Client.Product.Create().SetName("Parcel").SetSlug("topology").SetPrice(100).SetGoodsType("physical").SaveX(ctx)
	qty := int64(3)
	sku, e := r.CreateSku(ctx, SkuInput{ProductID: p.ID, Name: "Large", PhysicalStock: &qty})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.DeleteSku(ctx, sku.ID); e == nil {
		t.Fatal("stocked SKU deleted")
	}
	if e = data.AdjustPhysicalStock(ctx, d, p, 0, 4); e == nil {
		t.Fatal("base stock changed while SKUs exist")
	}
	zero := int64(0)
	if _, e = r.UpdateSku(ctx, sku.ID, SkuInput{PhysicalStock: &zero, ExpectedPhysicalStock: &qty}); e != nil {
		t.Fatal(e)
	}
	if e = r.DeleteSku(ctx, sku.ID); e != nil {
		t.Fatal(e)
	}
	o := d.Client.Order.Create().SetOrderNo("historical-base").SaveX(ctx)
	d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(100).SetAmount(100).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	if _, e = r.CreateSku(ctx, SkuInput{ProductID: p.ID, Name: "Blocked", PhysicalStock: &qty}); e == nil {
		t.Fatal("historical base-order inventory ownership changed")
	}
}
