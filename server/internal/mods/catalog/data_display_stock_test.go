package catalog

import (
	"context"
	"errors"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"sync"
	"testing"
	"time"
)

type displayLookupFunc func(context.Context, uint64, string) (int32, error)

func (f displayLookupFunc) DisplayStock(ctx context.Context, id uint64, code string) (int32, error) {
	return f(ctx, id, code)
}

func TestBothCatalogListsRefreshExpiredStock(t *testing.T) {
	d, _ := newStatsEnv(t)
	ctx := context.Background()
	repo := NewProductRepoImpl(d, nil)
	store := NewStoreCatalogService(NewCatalogUsecase(repo), nil, nil)
	admin := NewAdminCatalogService(repo, nil, nil, nil)
	ids := map[string]uint64{}
	for _, name := range []string{"stale", "unknown", "zero", "unlimited", "failed", "fresh"} {
		p := d.Client.Product.Create().SetName(name).SetSlug(name).SetStatus(1).SetStockVisible(true).SetUpstreamSourceID(9).SetUpstreamProductCode(name).SaveX(ctx)
		ids[name] = p.ID
		checked := time.Now().Add(-time.Hour)
		if name == "fresh" {
			checked = time.Now()
		}
		n := 7
		if name == "unknown" {
			n = -2
		}
		d.Client.SupplyMapping.Create().SetConnectionID(9).SetUpstreamProduct(name).SetLocalProductID(p.ID).SetUpStock(int32(n)).SetStockCheckedAt(checked).SaveX(ctx)
	}
	var mu sync.Mutex
	calls := map[string]int{}
	store.SetStockLookup(displayLookupFunc(func(_ context.Context, _ uint64, code string) (int32, error) {
		mu.Lock()
		calls[code]++
		mu.Unlock()
		switch code {
		case "zero":
			return 0, nil
		case "unlimited":
			return -1, nil
		case "failed":
			return -2, errors.New("upstream unavailable")
		}
		return 32, nil
	}))
	got, err := store.ListProducts(ctx, &storefrontv1.ListProductsRequest{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got.Items {
		want := int64(32)
		status := "current"
		switch p.Name {
		case "zero":
			want = 0
		case "unlimited":
			want = -1
		case "fresh":
			want = 7
		case "failed":
			want = -2
			status = "stale"
		}
		if p.Stock != want || p.StockStatus != status {
			t.Fatalf("store %s: %+v", p.Name, p)
		}
	}
	out, err := admin.ListProducts(ctx, &adminv1.ListProductsRequest{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range out.Products {
		if p.Id == ids["unknown"] && (p.Stock != 32 || p.StockStatus != "current") {
			t.Fatalf("admin: %+v", p)
		}
	}
	if calls["fresh"] != 0 || calls["unknown"] != 2 {
		t.Fatalf("cache policy: %+v", calls)
	}
}

func TestStockRefreshDeadlineKeepsReferenceAndBoundsConcurrency(t *testing.T) {
	d, _ := newStatsEnv(t)
	ctx := context.Background()
	repo := NewProductRepoImpl(d, nil)
	var ids []uint64
	for i := 0; i < 12; i++ {
		name := time.Now().Add(time.Duration(i)).String()
		p := d.Client.Product.Create().SetName(name).SetSlug(name).SetUpstreamSourceID(9).SetUpstreamProductCode(name).SaveX(ctx)
		ids = append(ids, p.ID)
		d.Client.SupplyMapping.Create().SetConnectionID(9).SetUpstreamProduct(name).SetLocalProductID(p.ID).SetUpStock(18).SetStockCheckedAt(time.Now().Add(-time.Hour)).SaveX(ctx)
	}
	var mu sync.Mutex
	active, peak, calls := 0, 0, 0
	repo.SetStockLookup(displayLookupFunc(func(ctx context.Context, _ uint64, _ string) (int32, error) {
		mu.Lock()
		active++
		calls++
		if active > peak {
			peak = active
		}
		mu.Unlock()
		<-ctx.Done()
		mu.Lock()
		active--
		mu.Unlock()
		return -2, ctx.Err()
	}))
	bounded, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	got, err := repo.StockSnapshotBatch(bounded, ids)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second || peak > 4 || calls > 4 {
		t.Fatalf("unbounded refresh: peak %d calls %d", peak, calls)
	}
	for _, s := range got {
		if s.Available != -2 || s.Quantity != 18 || s.Status != "stale" {
			t.Fatalf("lost reference: %+v", s)
		}
	}
}
