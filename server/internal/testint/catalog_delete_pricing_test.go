//go:build integration

package testint

import (
	"context"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supplier"
	"testing"
)

func TestCatalogDeleteAfterPricingMySQL(t *testing.T) { catalogDeleteAfterPricing(MySQL(t)) }
func TestCatalogDeleteAfterPricingPG(t *testing.T)    { catalogDeleteAfterPricing(PG(t)) }
func catalogDeleteAfterPricing(h *Harness) {
	t, d, ctx := h.T, h.Data, context.Background()
	repo := catalog.NewProductRepoImpl(d, nil)
	svc := catalog.NewAdminCatalogService(repo, nil, nil, nil)
	pricing := supplier.NewSupplierRepoImpl(d, nil)
	acc := d.Client.SupplierAccount.Create().SetName("fixture").SetAPIKey("fixture").SetAPISecret([]byte("fixture")).SaveX(ctx)
	cat := d.Client.Category.Create().SetName("fixture").SaveX(ctx)
	if e := pricing.UpsertPriceRule(ctx, acc.ID, 0, 0, cat.ID, "category", 0, 9000); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 10; i++ {
		p := d.Client.Product.Create().SetName("fixture").SetSlug(fmt.Sprintf("fixture-%d", i)).SetCategoryID(cat.ID).SetPrice(100).SaveX(ctx)
		// Repeated pricing and immediate deletion must not mistake unchanged rows
		// within MySQL's timestamp precision for an absent product.
		for j := 0; j < 3; j++ {
			if e := pricing.UpsertPriceRule(ctx, acc.ID, p.ID, 0, 0, "product", 80, 0); e != nil {
				t.Fatal(e)
			}
		}
		if _, e := svc.DeleteProduct(ctx, &adminv1.DeleteProductRequest{Id: p.ID}); e != nil {
			t.Fatal(e)
		}
		if _, e := svc.DeleteProduct(ctx, &adminv1.DeleteProductRequest{Id: p.ID}); e == nil {
			t.Fatal("archived product deleted twice")
		}
		if e := pricing.UpsertPriceRule(ctx, acc.ID, p.ID, 0, 0, "product", 80, 0); e == nil {
			t.Fatal("archived product accepted price rule")
		}
	}
	if _, e := svc.DeleteCategory(ctx, &adminv1.DeleteCategoryRequest{Id: cat.ID}); e != nil {
		t.Fatal(e)
	}
	if e := pricing.UpsertPriceRule(ctx, acc.ID, 0, 0, cat.ID, "category", 0, 9000); e == nil {
		t.Fatal("deleted category accepted pricing")
	}
	if d.Client.SupplierProductPrice.Query().CountX(ctx) != 0 {
		t.Fatal("deleted catalog retained price rules")
	}
}

func TestCategoryCascadeMySQL(t *testing.T) { categoryCascade(MySQL(t)) }
func TestCategoryCascadePG(t *testing.T)    { categoryCascade(PG(t)) }
func categoryCascade(h *Harness) {
	t, d, ctx := h.T, h.Data, context.Background()
	repo := catalog.NewProductRepoImpl(d, nil)
	svc := catalog.NewAdminCatalogService(repo, nil, nil, nil)
	root := d.Client.Category.Create().SetName("root").SaveX(ctx)
	child := d.Client.Category.Create().SetName("child").SetParentID(root.ID).SaveX(ctx)
	keep := d.Client.Category.Create().SetName("keep").SaveX(ctx)
	for i := 0; i < 12; i++ {
		p := d.Client.Product.Create().SetName("snapshot name").SetSlug(fmt.Sprint(i)).SetCategoryID(child.ID).SetIsLocked(true).SetStatus(int8(i % 3)).SetPrice(100).SaveX(ctx)
		o := d.Client.Order.Create().SetOrderNo(fmt.Sprint(i)).SetStatus("fulfilling").SetTotalAmount(100).SaveX(ctx)
		d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(100).SetAmount(100).SetFulfillmentType("auto").SaveX(ctx)
		d.Client.SupplierProductPrice.Create().SetSupplierAccountID(1).SetProductID(p.ID).SetPrice(80).SaveX(ctx)
	}
	if _, e := svc.DeleteCategory(ctx, &adminv1.DeleteCategoryRequest{Id: root.ID}); e != nil {
		t.Fatal(e)
	}
	if d.Client.Category.Query().CountX(ctx) != 1 || d.Client.Category.Query().OnlyX(ctx).ID != keep.ID || d.Client.Order.Query().CountX(ctx) != 12 || d.Client.SupplierProductPrice.Query().CountX(ctx) != 0 {
		t.Fatal("cascade/history/pricing cleanup failed")
	}
	for _, p := range d.Client.Product.Query().AllX(ctx) {
		if p.Status != -1 || p.CategoryID != 0 {
			t.Fatal("product not removed")
		}
	}
	for _, it := range d.Client.OrderItem.Query().AllX(ctx) {
		if it.ProductName != "snapshot name" || it.Amount != 100 {
			t.Fatal("snapshot corrupted")
		}
	}
}
