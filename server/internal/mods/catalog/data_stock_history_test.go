package catalog

import (
	"context"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

func TestStockHistoryDoesNotBecomeAvailable(t *testing.T) {
	d, admin := newStatsEnv(t)
	ctx := context.Background()
	repo := NewProductRepoImpl(d, nil)
	checked := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	makeProduct := func(name string, count int32, when time.Time) *ent.Product {
		p := d.Client.Product.Create().SetName(name).SetSlug(name).SetStatus(1).SetStockVisible(true).SetUpstreamSourceID(9).SetUpstreamProductCode(name).SaveX(ctx)
		q := d.Client.SupplyMapping.Create().SetConnectionID(9).SetLocalProductID(p.ID).SetUpstreamProduct(name).SetUpStock(count)
		if !when.IsZero() {
			q.SetStockCheckedAt(when)
		}
		q.SaveX(ctx)
		return p
	}
	stale := makeProduct("stale", 12, checked)
	zero := makeProduct("old-zero", 0, checked)
	unknown := makeProduct("unqueried", 999, time.Time{})
	failed := makeProduct("failed", -2, checked)
	fresh := makeProduct("fresh", 8, time.Now().UTC())
	unlimited := makeProduct("old-unlimited", -1, checked)
	rows := []*ent.Product{stale, zero, unknown, failed, fresh, unlimited}
	available, err := data.ProductStocks(ctx, d, rows)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range rows {
		want := int64(-2)
		if p.ID == fresh.ID {
			want = 8
		}
		if available[p.ID] != want {
			t.Fatalf("stock %s=%d", p.Name, available[p.ID])
		}
	}
	front, err := NewStoreCatalogService(NewCatalogUsecase(repo), nil, nil).ListProducts(ctx, &storefrontv1.ListProductsRequest{PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range front.Items {
		if p.Id == stale.ID && (p.Stock != -2 || p.StockStatus != "stale" || p.StockReference != 12 || p.StockCheckedAt != checked.Unix()) {
			t.Fatalf("store history: %v", p)
		}
		if p.Id == unknown.ID && (p.StockStatus != "unknown" || p.StockCheckedAt != 0 || p.StockReference != -2) {
			t.Fatalf("unverified quantity labeled known: %v", p)
		}
	}
	back, err := admin.ListProducts(ctx, &adminv1.ListProductsRequest{PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range back.Products {
		if p.Id == stale.ID && (p.Stock != -2 || p.StockReference != 12 || p.StockStatus != "stale") {
			t.Fatalf("admin history: %v", p)
		}
	}
	scoped, err := repo.StockSnapshotBatch(tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 99}), []uint64{stale.ID})
	if err != nil || len(scoped) != 0 {
		t.Fatal("stock history leaked across tenant")
	}
}
