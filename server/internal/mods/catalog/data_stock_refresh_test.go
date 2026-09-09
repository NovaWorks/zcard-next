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
