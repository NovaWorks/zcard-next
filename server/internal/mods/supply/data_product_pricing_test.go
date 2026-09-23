package supply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

func TestProductRulesSurviveAllSyncPaths(t *testing.T) {
	for _, scope := range []string{ScopePrice, ScopeCollect} {
		for _, tc := range []struct {
			mode         string
			percent      float64
			amount       int64
			product, sku int64
		}{
			{PriceModeFixed, 0, 300, 2100, 3600}, {PriceModePercent, 20, 0, 2160, 3960},
			{PriceModePercent, 0, 0, 1800, 3300}, {PriceModeEqual, 0, 0, 1800, 3300},
			{PriceModeChannel, 0, 0, 2080, 3730}, {PriceModePending, 0, 0, 0, 0},
		} {
			t.Run(fmt.Sprintf("%s/%s/%g", scope, tc.mode, tc.percent), func(t *testing.T) {
				ctx := context.Background()
				repo, d := newTestRepo(t)
				conn := mustConn(t, repo, d, "rules")
				conn = d.Client.SupplyConnection.UpdateOneID(conn.ID).SetExchangeRate(1.5).SetPriceMarkupPercent(10).SetPriceMarkupAmount(100).SaveX(ctx)
				writer := catalog.NewProductRepoImpl(d, nil)
				s := &SyncService{repo: repo, writer: writer, maintainer: writer, log: slog.Default()}
				p := pricingProduct()
				if _, err := s.ImportOne(ctx, conn, p, nil, tc.mode, tc.percent, tc.amount); err != nil {
					t.Fatal(err)
				}
				p.Price = 1200
				p.FactoryPrice = 1200
				p.SKUs[0].Price = 2200
				for i := 0; i < 2; i++ {
					if _, err := s.syncOne(ctx, 0, &ent.SupplySyncTask{Scope: scope}, conn, p, nil, &TaskProgress{}); err != nil {
						t.Fatal(err)
					}
				}
				m, _ := repo.GetMapping(ctx, conn.ID, p.ID, "")
				got := d.Client.Product.GetX(ctx, m.LocalProductID)
				if got.Price != tc.product || got.FactoryPrice != 1800 {
					t.Fatalf("product price/cost=%d/%d want=%d/1800", got.Price, got.FactoryPrice, tc.product)
				}
				skus := d.Client.ProductSku.Query().Where(productsku.ProductID(got.ID)).AllX(ctx)
				if tc.mode == PriceModePending {
					if got.Status != 0 || len(skus) != 0 {
						t.Fatal("pending product was priced or enabled")
					}
				} else if len(skus) != 1 || skus[0].Price != tc.sku {
					t.Fatalf("SKU price=%v want=%d", skus, tc.sku)
				}
				if _, err := readProductRule(m.PricingOverride); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func pricingProduct() *adapter.Product {
	return &adapter.Product{ID: "P", Name: "product", Price: 1000, FactoryPrice: 1000, Stock: 8, IsActive: true,
		SKUs: []adapter.SKU{{ID: "S", Code: "S", Name: "size S", Price: 2000, Stock: 8, IsActive: true, SpecValues: map[string]string{"size": "S"}}}}
}

func TestSKUManualPriceAndLegacyProtection(t *testing.T) {
	for _, scope := range []string{ScopePrice, ScopeCollect} {
		t.Run(scope, func(t *testing.T) {
			ctx := context.Background()
			repo, d := newTestRepo(t)
			conn := mustConn(t, repo, d, "manual")
			writer := catalog.NewProductRepoImpl(d, nil)
			s := &SyncService{repo: repo, writer: writer, maintainer: writer, log: slog.Default()}
			p := pricingProduct()
			if _, err := s.ImportOne(ctx, conn, p, nil, PriceModeFixed, 0, 300); err != nil {
				t.Fatal(err)
			}
			m, _ := repo.GetMapping(ctx, conn.ID, p.ID, "")
			sku := d.Client.ProductSku.Query().Where(productsku.ProductID(m.LocalProductID)).OnlyX(ctx)
			d.Client.ProductSku.UpdateOneID(sku.ID).SetPrice(2900).ExecX(ctx)
			p.Price = 1200
			p.SKUs[0].Price = 2200
			p.SKUs[0].Name = "renamed S"
			for i := 0; i < 2; i++ {
				if _, err := s.syncOne(ctx, 0, &ent.SupplySyncTask{Scope: scope}, conn, p, nil, &TaskProgress{}); err != nil {
					t.Fatal(err)
				}
			}
			if d.Client.Product.GetX(ctx, m.LocalProductID).Price != 1500 || d.Client.ProductSku.GetX(ctx, sku.ID).Price != 2900 {
				t.Fatal("manual SKU not protected independently")
			}
			if _, err := s.syncOne(ctx, 0, &ent.SupplySyncTask{Scope: scope, ForceReprice: true}, conn, p, nil, &TaskProgress{}); err != nil {
				t.Fatal(err)
			}
			if d.Client.ProductSku.GetX(ctx, sku.ID).Price != 2500 {
				t.Fatal("force must use saved +300, not channel pricing")
			}
			// Historical rows lack enough information to recover the original rule.
			m, _ = repo.GetMapping(ctx, conn.ID, p.ID, "")
			m.PricingOverride = map[string]any{"last_synced_price": 1500}
			if err := repo.UpsertMapping(ctx, m); err != nil {
				t.Fatal(err)
			}
			p.Price = 1800
			p.SKUs[0].Price = 2800
			if _, err := s.syncOne(ctx, 0, &ent.SupplySyncTask{Scope: scope, ForceReprice: true}, conn, p, nil, &TaskProgress{}); err != nil {
				t.Fatal(err)
			}
			if d.Client.Product.GetX(ctx, m.LocalProductID).Price != 1500 || d.Client.ProductSku.GetX(ctx, sku.ID).Price != 2500 {
				t.Fatal("historical rule was guessed")
			}
			// Explicit reimport establishes the operator's chosen rule.
			if _, err := s.ImportOne(ctx, conn, p, nil, PriceModePercent, 20, 0); err != nil {
				t.Fatal(err)
			}
			if d.Client.Product.GetX(ctx, m.LocalProductID).Price != 2160 {
				t.Fatal("explicit rule reset failed")
			}
		})
	}
}

func TestPricingTransactionRollsBackWithMapping(t *testing.T) {
	for _, scope := range []string{ScopePrice, ScopeCollect} {
		t.Run(scope, func(t *testing.T) {
			ctx := context.Background()
			repo, d := newTestRepo(t)
			conn := mustConn(t, repo, d, "rollback")
			writer := catalog.NewProductRepoImpl(d, nil)
			s := &SyncService{repo: repo, writer: writer, maintainer: writer, log: slog.Default()}
			p := pricingProduct()
			if _, err := s.ImportOne(ctx, conn, p, nil, PriceModeFixed, 0, 300); err != nil {
				t.Fatal(err)
			}
			m, _ := repo.GetMapping(ctx, conn.ID, p.ID, "")
			sku := d.Client.ProductSku.Query().Where(productsku.ProductID(m.LocalProductID)).OnlyX(ctx)
			d.Client.SupplyMapping.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, errors.New("mapping write failed") })
			})
			p.Price = 1500
			p.FactoryPrice = 1500
			p.SKUs[0].Price = 2500
			stats := &TaskProgress{}
			if _, err := s.syncOne(ctx, 0, &ent.SupplySyncTask{Scope: scope}, conn, p, nil, stats); err == nil {
				t.Fatal("expected write failure")
			}
			if d.Client.Product.GetX(ctx, m.LocalProductID).Price != 1300 || d.Client.Product.GetX(ctx, m.LocalProductID).FactoryPrice != 1000 || d.Client.ProductSku.GetX(ctx, sku.ID).Price != 2300 {
				t.Fatal("partial price/cost write escaped transaction")
			}
			if stats.Processed != 0 || stats.PriceUpdated != 0 {
				t.Fatal("failed transaction counted as updated")
			}
		})
	}
}

type failingQuoteUpstream struct{ *fakeUpstream }

func (*failingQuoteUpstream) QuoteProduct(context.Context, *adapter.Product) (*adapter.Product, error) {
	return nil, errors.New("quote unavailable")
}

func TestQuoteFailurePreservesProductAndDoesNotReconcile(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "quote failure")
	writer := catalog.NewProductRepoImpl(d, nil)
	s := &SyncService{repo: repo, writer: writer, maintainer: writer, log: slog.Default()}
	p := pricingProduct()
	if _, err := s.ImportOne(ctx, conn, p, nil, PriceModeFixed, 0, 300); err != nil {
		t.Fatal(err)
	}
	m, _ := repo.GetMapping(ctx, conn.ID, p.ID, "")
	other := d.Client.Product.Create().SetName("other").SetSlug("other").SetPrice(999).SetStatus(1).SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("missing").SaveX(ctx)
	task, _ := repo.CreateSyncTask(ctx, conn.ID, "full", ScopeCollect, false)
	up := &failingQuoteUpstream{&fakeUpstream{products: []adapter.Product{*p}, total: 1, echo: true}}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := s.runLoop(runCtx, task.ID, task, conn, up, loadScheduleSettings(conn), ScopeCollect, false, listOf(up)); err != nil {
		t.Fatal(err)
	}
	cancel()
	got, _ := repo.GetSyncTask(ctx, task.ID)
	if got.ErrorCode != "PRICE_QUOTE_FAILED" || got.Status != "failed" {
		t.Fatalf("quote failure hidden: %v", got)
	}
	if d.Client.Product.GetX(ctx, m.LocalProductID).Price != 1300 || d.Client.Product.GetX(ctx, other.ID).Status != 1 {
		t.Fatal("failed quote changed prices or reconciled deletion")
	}
}

func TestPricingRejectsChangedConnectionAndStalePreview(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "snapshot")
	writer := catalog.NewProductRepoImpl(d, nil)
	s := &SyncService{repo: repo, writer: writer, maintainer: writer, log: slog.Default()}
	p := pricingProduct()
	if _, err := s.ImportOne(ctx, conn, p, nil, PriceModeChannel, 0, 0); err != nil {
		t.Fatal(err)
	}
	m, _ := repo.GetMapping(ctx, conn.ID, p.ID, "")
	d.Client.SupplyConnection.UpdateOneID(conn.ID).SetPriceMarkupAmount(500).ExecX(ctx)
	for _, scope := range []string{ScopePrice, ScopeCollect} {
		if _, err := s.syncOne(ctx, 0, &ent.SupplySyncTask{Scope: scope}, conn, p, nil, &TaskProgress{}); err == nil {
			t.Fatal("stale connection pricing was committed")
		}
	}
	if d.Client.Product.GetX(ctx, m.LocalProductID).Price != 1000 {
		t.Fatal("stale task changed price")
	}
	previewCache.Lock()
	previewCache.m[conn.ID] = previewEntry{identity: previewIdentity(conn), at: time.Now(), byCode: map[string]adapter.Product{p.ID: *p}}
	previewCache.Unlock()
	t.Cleanup(func() { previewCache.Lock(); delete(previewCache.m, conn.ID); previewCache.Unlock() })
	// An unusable new credential makes refresh fail before network IO; a stale
	// cache hit would incorrectly conceal that failure and reuse the old account.
	d.Client.SupplyConnection.UpdateOneID(conn.ID).SetCredentials([]byte("invalid-sealed-fixture")).ExecX(ctx)
	if _, err := NewAdminSupplyService(repo, s).loadPreview(ctx, conn.ID); err == nil {
		t.Fatal("old account preview reused")
	}
	if _, err := s.ImportOne(ctx, conn, p, nil, PriceModeEqual, 0, 0); err == nil {
		t.Fatal("old account import committed")
	}
}

func TestMappingEditPreservesRulesAndValidatesExplicitChanges(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "mapping rules")
	writer := catalog.NewProductRepoImpl(d, nil)
	s := &SyncService{repo: repo, writer: writer, maintainer: writer, log: slog.Default()}
	p := pricingProduct()
	if _, err := s.ImportOne(ctx, conn, p, nil, PriceModeFixed, 0, 300); err != nil {
		t.Fatal(err)
	}
	m, _ := repo.GetMapping(ctx, conn.ID, p.ID, "")
	svc := NewAdminSupplyService(repo, s)
	req := &adminv1.UpsertMappingRequest{ConnectionId: conn.ID, UpstreamProduct: p.ID, LocalProductId: m.LocalProductID}
	for _, raw := range []string{"", `{}`, `{"last_synced_price":1,"sku_prices":{"S":1}}`} {
		req.PricingOverride = raw
		if _, err := svc.UpsertMapping(ctx, req); err != nil {
			t.Fatal(err)
		}
		fresh, _ := repo.GetMapping(ctx, conn.ID, p.ID, "")
		rule, err := readProductRule(fresh.PricingOverride)
		if err != nil || rule == nil || rule.Mode != PriceModeFixed || rule.Amount != 300 || toInt64(fresh.PricingOverride["last_synced_price"]) != 1300 {
			t.Fatal("mapping edit erased rule/baseline")
		}
	}
	for _, raw := range []string{`[]`, `{"price":-1}`, `{"rule":{"mode":"invalid"}}`, `{"rule":{"mode":"fixed","amount":1.5}}`} {
		req.PricingOverride = raw
		if _, err := svc.UpsertMapping(ctx, req); err == nil {
			t.Fatalf("invalid rule accepted: %s", raw)
		}
	}
}

func TestUnpricedImportAndDisabledCollectionStaySafe(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "unpriced")
	writer := catalog.NewProductRepoImpl(d, nil)
	s := &SyncService{repo: repo, writer: writer, maintainer: writer, log: slog.Default()}
	p := pricingProduct()
	p.SKUs[0].Price = 0
	if _, err := s.ImportOne(ctx, conn, p, nil, PriceModeFixed, 0, 300); err == nil {
		t.Fatal("invalid zero SKU quote accepted just because markup makes it positive")
	}
	if d.Client.Product.Query().CountX(ctx) != 0 {
		t.Fatal("invalid quote imported partially")
	}
	conn = d.Client.SupplyConnection.UpdateOneID(conn.ID).SetAutoSyncPrice(false).SaveX(ctx)
	p = pricingProduct()
	if _, err := s.syncOne(ctx, 0, collectTask(), conn, p, nil, &TaskProgress{}); err != nil {
		t.Fatal(err)
	}
	m, _ := repo.GetMapping(ctx, conn.ID, p.ID, "")
	got := d.Client.Product.GetX(ctx, m.LocalProductID)
	if got.Price != 0 || got.Status != 0 {
		t.Fatal("collection listed an unpriced product")
	}
}
