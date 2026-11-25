package supply

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

type catalogStockUpstream struct {
	*fakeUpstream
	reads atomic.Int32
}

func (a *catalogStockUpstream) GetStock(context.Context, string, string) (int32, error) {
	a.reads.Add(1)
	return -2, adapter.ErrRateLimited
}

func TestACGStatusCatalogStockHandlesLargeBatchWithoutSKUProbes(t *testing.T) {
	r, d := newTestRepo(t)
	ctx := context.Background()
	c := mustConn(t, r, d, "catalog-stock")
	c = d.Client.SupplyConnection.UpdateOneID(c.ID).SetDriver("acg_faka").SaveX(ctx)
	w := catalog.NewProductRepoImpl(d, nil)
	s := &SyncService{repo: r, writer: w, maintainer: w, log: slog.Default()}
	var products []adapter.Product
	var firstID uint64
	for i := 0; i < 803; i++ {
		p := adapter.Product{ID: fmt.Sprintf("P%04d", i), Name: "ACG", Price: 100, FactoryPrice: 100, Stock: 773, IsActive: true, StockFromCatalog: true}
		if i == 0 {
			original := p
			original.SKUs = []adapter.SKU{{ID: "美国|", Code: "美国|", Name: "美国", Price: 100, Stock: 8, IsActive: true}}
			if _, err := s.ImportOne(ctx, c, &original, nil, PriceModeEqual, 0, 0); err != nil {
				t.Fatal(err)
			}
			m, _ := r.GetMapping(ctx, c.ID, p.ID, "")
			firstID = m.LocalProductID
		}
		products = append(products, p)
	}
	a := &catalogStockUpstream{fakeUpstream: &fakeUpstream{products: products, total: len(products), echo: true}}
	task, _ := r.CreateSyncTask(ctx, c.ID, "full", ScopeStatus, false)
	if err := s.runLoop(ctx, task.ID, task, c, a, loadScheduleSettings(c), ScopeStatus, false, listOf(a)); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetSyncTask(ctx, task.ID)
	m, _ := r.GetMapping(ctx, c.ID, products[0].ID, "")
	if got.Status != "done" || a.reads.Load() != 0 || m.UpStock != 773 || d.Client.Product.GetX(ctx, firstID).Price != 100 {
		t.Fatalf("stock=%d queries=%d task=%+v", m.UpStock, a.reads.Load(), got)
	}
	sku := d.Client.ProductSku.Query().Where(productsku.ProductID(firstID)).OnlyX(ctx)
	if sku.Price != 100 || sku.UpstreamSkuID != "美国|" {
		t.Fatal("aggregate stock maintenance modified an existing SKU", sku)
	}
	if rows := d.Client.SupplyMapping.Query().AllX(ctx); len(rows) != 1 {
		t.Fatal("aggregate stock was copied to per-SKU mappings", rows)
	}
}

func TestACGCatalogPolicyAndStockFallback(t *testing.T) {
	for _, mode := range []any{nil, "", "catalog", "live", "typo", true, map[string]any{}} {
		conn := &ent.SupplyConnection{Driver: "acg_faka", Settings: map[string]any{"acg_stock_source": mode}}
		want := mode == nil || mode == "" || mode == "catalog"
		if preferACGListStock(conn) != want {
			t.Fatalf("unexpected policy for %v", mode)
		}
		conn.Driver = "zcard"
		if preferACGListStock(conn) {
			t.Fatal("ACG policy leaked into another driver")
		}
	}
	for _, tc := range []struct {
		name    string
		stock   int32
		catalog bool
		reads   int32
	}{
		{"zero", 0, true, 0}, {"positive", 773, true, 0},
		{"missing", -2, false, 1}, {"unlimited-needs-confirmation", -1, false, 1},
		{"strict-live", 773, false, 1}, {"invalid-marked-catalog", -2, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, _ := newTestSyncService(t)
			a := &catalogStockUpstream{fakeUpstream: &fakeUpstream{}}
			items := []adapter.Product{{ID: "P", IsActive: true, Stock: tc.stock, StockFromCatalog: tc.catalog}}
			err := s.backfillCatalogStocks(context.Background(), a, loadScheduleSettings(nil), items, 0, true)
			if a.reads.Load() != tc.reads || (err != nil) != (tc.reads > 0) {
				t.Fatalf("queries=%d err=%v", a.reads.Load(), err)
			}
		})
	}
}

func TestACGPriceSyncSkipsUnsupportedProductsAndPreservesPrices(t *testing.T) {
	r, d := newTestRepo(t)
	ctx := context.Background()
	c := mustConn(t, r, d, "price-skips")
	w := catalog.NewProductRepoImpl(d, nil)
	s := &SyncService{repo: r, writer: w, maintainer: w, log: slog.Default()}
	var products []adapter.Product
	for _, code := range []string{"A", "manual", "draft", "delisted", "E"} {
		p := adapter.Product{ID: code, Name: code, Price: 100, FactoryPrice: 100, IsActive: true, Stock: 10}
		if _, err := s.ImportOne(ctx, c, &p, nil, PriceModeEqual, 0, 0); err != nil {
			t.Fatal(err)
		}
		products = append(products, p)
	}
	task, _ := r.CreateSyncTask(ctx, c.ID, "full", ScopePrice, false)
	a := &isolatedQuoteUpstream{fakeUpstream: &fakeUpstream{products: products, total: len(products), echo: true}, failures: map[string]error{"manual": adapter.ErrNotSupported, "draft": adapter.ErrNotSupported, "delisted": adapter.ErrProductUnavailable}}
	if err := s.runLoop(ctx, task.ID, task, c, a, loadScheduleSettings(c), ScopePrice, false, listOf(a)); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetSyncTask(ctx, task.ID)
	if got.Status != "done" || got.ErrorCode != "" || got.ManualSkippedCount != 3 || got.PriceUpdatedCount != 2 || got.ProcessedCount != 5 {
		t.Fatal("unsupported products failed the batch", got)
	}
	for _, code := range []string{"manual", "draft", "delisted"} {
		m, _ := r.GetMapping(ctx, c.ID, code, "")
		if p := d.Client.Product.GetX(ctx, m.LocalProductID); p.Price != 100 || p.FactoryPrice != 100 {
			t.Fatal("skipped product price/cost changed", p)
		}
	}
}
