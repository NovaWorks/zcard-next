package catalog

import (
	"context"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	couponmod "github.com/NovaWorks/zcard-next/server/internal/mods/coupon"
	"testing"
	"time"
)

func TestCatalogFlashDisplayAndSkuFallback(t *testing.T) {
	d, _ := newStatsEnv(t)
	ctx := context.Background()
	repo := NewProductRepoImpl(d, nil)
	svc := NewStoreCatalogService(NewCatalogUsecase(repo), nil, nil)
	flash := couponmod.NewCouponRepoImpl(d)
	svc.SetFlashReader(flash)
	p := d.Client.Product.Create().SetName("flash").SetSlug("flash").SetPrice(3000).SetStatus(1).SetStockType("url").SaveX(ctx)
	sku := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("variant").SetPrice(4000).SetSpecValues(map[string]string{}).SaveX(ctx)
	now := time.Now().UTC()
	offer, err := flash.CreateFlash(ctx, p.ID, 0, 2000, now.Add(-time.Hour), now.Add(time.Hour), 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListProducts(ctx, &storefrontv1.ListProductsRequest{})
	if err != nil || len(list.Items) != 1 || list.Items[0].FlashSale.GetPriceCents() != 2000 || list.Items[0].PriceCents != 3000 {
		t.Fatalf("list: %+v %v", list, err)
	}
	detail, err := svc.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: p.ID})
	if err != nil || detail.FlashSale.GetRemaining() != 10 || len(detail.Skus) != 1 || detail.Skus[0].FlashSale.GetPriceCents() != 2000 {
		t.Fatalf("detail: %+v %v", detail, err)
	}
	specific, err := flash.CreateFlash(ctx, p.ID, sku.ID, 1500, now.Add(-time.Hour), now.Add(time.Hour), 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	detail, err = svc.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: p.ID})
	if err != nil || detail.Skus[0].FlashSale.GetPriceCents() != 1500 {
		t.Fatal("specific SKU did not override product offer")
	}
	// Expired/future offers do not override the active product campaign.
	d.Client.FlashSale.UpdateOneID(specific.ID).SetStartAt(now.Add(time.Hour)).SetEndAt(now.Add(2 * time.Hour)).SaveX(ctx)
	detail, err = svc.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: p.ID})
	if err != nil || detail.Skus[0].FlashSale.GetPriceCents() != 2000 {
		t.Fatal("future offer masked active campaign")
	}
	d.Client.FlashSale.UpdateOneID(offer.ID).SetEndAt(now.Add(-time.Second)).SaveX(ctx)
	detail, err = svc.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: p.ID})
	if err != nil || detail.FlashSale != nil || detail.Skus[0].FlashSale != nil {
		t.Fatal("expired offer displayed")
	}
}
