package catalog

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"testing"
)

func TestSkuCreateDisplayResetAndOwnership(t *testing.T) {
	d, admin := newStatsEnv(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("sku product").SetSlug("sku-product").SetPrice(1000).SetStatus(1).SetStockType("url").SaveX(ctx)
	a, err := admin.CreateSku(ctx, &adminv1.CreateSkuRequest{ProductId: p.ID, Name: "monthly", PriceCents: 500, SpecValues: map[string]string{"term": "month"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := admin.CreateSku(ctx, &adminv1.CreateSkuRequest{ProductId: p.ID, Name: "annual", PriceCents: 2000, SpecValues: map[string]string{"term": "year"}})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewProductRepoImpl(d, nil)
	svc := NewStoreCatalogService(NewCatalogUsecase(repo), nil, nil)
	detail, err := svc.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: p.ID})
	if err != nil || len(detail.Skus) != 2 || detail.Skus[0].PriceCents != 500 || detail.Skus[1].PriceCents != 2000 {
		t.Fatalf("sku not exposed %+v %v", detail, err)
	}
	zero := int64(0)
	if _, err = admin.UpdateSku(ctx, &adminv1.UpdateSkuRequest{Id: a.Id, PriceCents: &zero}); err != nil {
		t.Fatal(err)
	}
	if price, err := repo.ResolvePrice(ctx, p.ID, a.Id); err != nil || price != 1000 {
		t.Fatalf("zero did not inherit %d %v", price, err)
	}
	if _, err := repo.ResolvePrice(ctx, p.ID, 0); err == nil {
		t.Fatal("missing SKU allowed")
	}
	if _, err := repo.ResolvePrice(ctx, p.ID+1, b.Id); err == nil {
		t.Fatal("foreign SKU allowed")
	}
	if _, err := repo.ResolvePrice(ctx, p.ID, 9999); err == nil {
		t.Fatal("deleted SKU allowed")
	}
}
