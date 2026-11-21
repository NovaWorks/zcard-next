package catalog

import (
	"context"
	"errors"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"testing"
	"time"
)

type stockLookupStub struct {
	n   int32
	err error
}

func (s stockLookupStub) DisplayStock(context.Context, uint64, string) (int32, error) {
	return s.n, s.err
}

func TestProductDetailRefreshesStaleUpstreamStock(t *testing.T) {
	d, _ := newStatsEnv(t)
	repo := NewProductRepoImpl(d, nil)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("stock refresh").SetSlug("stock-refresh").SetStatus(1).SetStockVisible(true).SetUpstreamSourceID(9).SetUpstreamProductCode("up").SaveX(ctx)
	d.Client.SupplyMapping.Create().SetConnectionID(9).SetUpstreamProduct("up").SetLocalProductID(p.ID).SetUpStock(20).SetStockCheckedAt(time.Now().UTC().Add(-time.Hour)).SaveX(ctx)
	cached, err := repo.StockBatch(ctx, []uint64{p.ID})
	if err != nil || cached[p.ID] != -2 {
		t.Fatal("stale cache advertised as available")
	}
	svc := NewStoreCatalogService(NewCatalogUsecase(repo), nil, nil)
	svc.SetStockLookup(stockLookupStub{n: 0})
	got, err := svc.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: p.ID})
	if err != nil || got.Stock != 0 {
		t.Fatalf("upstream zero not shown: %v", err)
	}
	svc.SetStockLookup(stockLookupStub{err: errors.New("upstream timeout")})
	got, err = svc.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: p.ID})
	if err != nil || got.Stock != -2 {
		t.Fatal("query failure must not become zero or unlimited")
	}
}

type skuStockLookupStub struct {
	stockLookupStub
	values map[string]int32
}

func (s skuStockLookupStub) DisplaySKUStock(_ context.Context, _ uint64, _, sku string) (int32, error) {
	n, ok := s.values[sku]
	if !ok {
		return -2, errors.New("upstream timeout")
	}
	return n, nil
}

func TestProductDetailUsesIndependentUpstreamSKUStock(t *testing.T) {
	d, _ := newStatsEnv(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("mcy skus").SetSlug("mcy-skus").SetStatus(1).SetUpstreamSourceID(9).SetUpstreamProductCode("12").SaveX(ctx)
	for _, code := range []string{"51", "52"} {
		sk := d.Client.ProductSku.Create().SetProductID(p.ID).SetName(code).SetSpecValues(map[string]string{}).SetUpstreamSkuID(code).SaveX(ctx)
		d.Client.SupplyMapping.Create().SetConnectionID(9).SetLocalProductID(p.ID).SetLocalSkuID(sk.ID).SetUpstreamProduct("12").SetUpstreamSku(code).SetUpStock(7).SetStockCheckedAt(time.Now().Add(-time.Hour)).SaveX(ctx)
	}
	svc := NewStoreCatalogService(NewCatalogUsecase(NewProductRepoImpl(d, nil)), nil, nil)
	for _, values := range []map[string]int32{{"51": 9, "52": 0}, {"51": 9}} {
		svc.SetStockLookup(skuStockLookupStub{stockLookupStub: stockLookupStub{n: 99}, values: values})
		got, err := svc.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: p.ID})
		if err != nil || len(got.Skus) != 2 || got.Skus[0].Stock != 9 {
			t.Fatalf("detail: %+v %v", got, err)
		}
		want := int64(0)
		if len(values) == 1 {
			want = -2
		}
		if got.Skus[1].Stock != want {
			t.Fatalf("SKU used stale/aggregate stock: %+v", got.Skus[1])
		}
	}
	// Without a live lookup, stale SKU mappings remain unknown.
	svc.SetStockLookup(stockLookupStub{n: 99})
	got, err := svc.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: p.ID})
	if err != nil || got.Skus[0].Stock != -2 || got.Skus[1].Stock != -2 {
		t.Fatalf("stale SKUs became available: %+v %v", got, err)
	}
}
