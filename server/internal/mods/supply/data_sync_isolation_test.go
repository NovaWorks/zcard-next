package supply

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

type isolatedQuoteUpstream struct {
	*fakeUpstream
	failures   map[string]error
	quoted     []string
	stockReads int
	stockErr   error
}

func (a *isolatedQuoteUpstream) QuoteProduct(_ context.Context, p *adapter.Product) (*adapter.Product, error) {
	a.quoted = append(a.quoted, p.ID)
	if err := a.failures[p.ID]; err != nil {
		return nil, err
	}
	copy := *p
	copy.Price, copy.FactoryPrice = 200, 200
	return &copy, nil
}
func (a *isolatedQuoteUpstream) GetStock(context.Context, string, string) (int32, error) {
	a.stockReads++
	return -2, a.stockErr
}

func TestSyncQuoteFailuresIsolateProductsAndProtectReconciliation(t *testing.T) {
	for _, scope := range []string{ScopeCollect, ScopePrice} {
		t.Run(scope, func(t *testing.T) {
			repo, d := newTestRepo(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn := mustConn(t, repo, d, "partial")
			writer := catalog.NewProductRepoImpl(d, nil)
			s := &SyncService{repo: repo, writer: writer, maintainer: writer, log: slog.Default()}
			products := []adapter.Product{}
			ids := map[string]uint64{}
			for _, code := range []string{"A", "B", "C"} {
				p := adapter.Product{ID: code, Name: code, Price: 100, FactoryPrice: 100, IsActive: true, Stock: 10}
				if code == "B" {
					p.SKUs = []adapter.SKU{{ID: "美国|", Code: "美国|", Name: "美国", Price: 100, Stock: 10, IsActive: true}}
				}
				if _, err := s.ImportOne(ctx, conn, &p, nil, PriceModeEqual, 0, 0); err != nil {
					t.Fatal(err)
				}
				m, _ := repo.GetMapping(ctx, conn.ID, code, "")
				ids[code] = m.LocalProductID
				products = append(products, p)
			}
			missing := d.Client.Product.Create().SetName("missing").SetSlug("missing").SetPrice(300).SetStatus(1).SetAutoListing(true).SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("missing").SaveX(ctx)
			task, _ := repo.CreateSyncTask(ctx, conn.ID, "full", scope, false)
			up := &isolatedQuoteUpstream{fakeUpstream: &fakeUpstream{products: products, total: 3, echo: true}, failures: map[string]error{"B": fmt.Errorf("invalid account quote")}}
			if err := s.runLoop(ctx, task.ID, task, conn, up, loadScheduleSettings(conn), scope, false, listOf(up)); err != nil {
				t.Fatal(err)
			}
			got, _ := repo.GetSyncTask(ctx, task.ID)
			if got.Status != "failed" || got.ErrorCode != "PARTIAL_SYNC_FAILED" || got.ProcessedCount != 3 || got.PriceUpdatedCount != 2 || !strings.Contains(got.ErrorContext, "失败 1") {
				t.Fatalf("partial result: %+v", got)
			}
			for code, want := range map[string]int64{"A": 200, "B": 100, "C": 200} {
				p := d.Client.Product.GetX(ctx, ids[code])
				if p.Price != want || p.FactoryPrice != want {
					t.Fatalf("%s price/cost=%d/%d want %d", code, p.Price, p.FactoryPrice, want)
				}
			}
			if d.Client.Product.GetX(ctx, missing.ID).Status != 1 {
				t.Fatal("partial quote run reconciled missing products")
			}
			sku := d.Client.ProductSku.Query().Where(productsku.ProductID(ids["B"])).OnlyX(ctx)
			if sku.UpstreamSkuID != "美国|" || sku.Price != 100 {
				t.Fatalf("failed quote changed a valid race SKU: %+v", sku)
			}
		})
	}
}

func TestSyncReadFailuresSkipUnsupportedButStopRateLimitsAndAuth(t *testing.T) {
	for _, tc := range []struct {
		name, status, code string
		failure            error
		quotes, skipped    int
	}{
		{"unsupported", "done", "", adapter.ErrNotSupported, 3, 1},
		{"unavailable", "done", "", adapter.ErrProductUnavailable, 3, 1},
		{"limited", "failed", "RATE_LIMITED", adapter.ErrRateLimited, 2, 0},
		{"credentials", "failed", "CREDENTIALS_INVALID", adapter.ErrCredentialsInvalid, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, _, _ := newTestSyncService(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn := mustConn(t, repo, nil, tc.name)
			task, _ := repo.CreateSyncTask(ctx, conn.ID, "full", ScopeCollect, false)
			up := &isolatedQuoteUpstream{fakeUpstream: &fakeUpstream{products: []adapter.Product{{ID: "A", Name: "A", IsActive: true, Stock: 1}, {ID: "B", Name: "B", IsActive: true, Stock: 1}, {ID: "C", Name: "C", IsActive: true, Stock: 1}}, total: 3, echo: true}, failures: map[string]error{"B": tc.failure}}
			if err := s.runLoop(ctx, task.ID, task, conn, up, loadScheduleSettings(conn), ScopeCollect, false, listOf(up)); err != nil {
				t.Fatal(err)
			}
			got, _ := repo.GetSyncTask(ctx, task.ID)
			if string(got.Status) != tc.status || got.ErrorCode != tc.code || len(up.quoted) != tc.quotes || int(got.ManualSkippedCount) != tc.skipped || got.PriceUpdatedCount < 1 {
				t.Fatalf("result: %+v quotes=%v", got, up.quoted)
			}
			if tc.skipped > 0 && !strings.Contains(got.ErrorContext, "跳过不可采购 1") {
				t.Fatal("skips not explained")
			}
		})
	}
}

func TestACGInactiveCatalogStockIsSkippedAndDelistingIsConfirmed(t *testing.T) {
	for _, inactive := range []bool{true, false} {
		s, repo, _, _ := newTestSyncService(t)
		ctx := context.Background()
		conn := mustConn(t, repo, nil, fmt.Sprint(inactive))
		conn = repo.entClient(ctx).SupplyConnection.UpdateOneID(conn.ID).SetDriver("acg_faka").SaveX(ctx)
		task, _ := repo.CreateSyncTask(ctx, conn.ID, "full", ScopeStatus, false)
		up := &isolatedQuoteUpstream{fakeUpstream: &fakeUpstream{products: []adapter.Product{{ID: "P", Name: "P", IsActive: !inactive, Stock: 999}}, total: 1, echo: true}, stockErr: adapter.ErrProductUnavailable}
		if err := s.runLoop(ctx, task.ID, task, conn, up, loadScheduleSettings(conn), ScopeStatus, false, listOf(up)); err != nil {
			t.Fatal(err)
		}
		got, _ := repo.GetSyncTask(ctx, task.ID)
		wantReads := 1
		if inactive {
			wantReads = 0
		}
		if got.Status != "done" || up.stockReads != wantReads {
			t.Fatalf("inactive=%v reads=%d result=%+v", inactive, up.stockReads, got)
		}
	}
}

func TestMaintenanceSettingsUnitsAndRetryProgress(t *testing.T) {
	settings := loadScheduleSettings(&ent.SupplyConnection{Settings: map[string]any{"schedule": map[string]any{"request_delay": 500, "quote_request_delay_ms": 500, "stock_request_delay_ms": 200}}})
	if settings.PageDelay != 60*time.Second || settings.QuoteDelay != 500*time.Millisecond || settings.StockBatchDelay != 200*time.Millisecond {
		t.Fatalf("units/bounds: %+v", settings)
	}
	repo, _ := newTestRepo(t)
	ctx := context.Background()
	conn := mustConn(t, repo, nil, "retry")
	task, _ := repo.CreateSyncTask(ctx, conn.ID, "full", ScopePrice, false)
	_, _ = repo.TouchTask(ctx, task.ID, TaskProgress{Processed: 20, PriceUpdated: 10})
	_ = repo.FinishTask(ctx, task.ID, "failed", "PRICE_QUOTE_FAILED", "old failure")
	if err := repo.SetTaskProcessing(ctx, task.ID, 3); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetSyncTask(ctx, task.ID)
	if got.ProcessedCount != 0 || got.PriceUpdatedCount != 0 || got.ErrorCode != "" || got.ErrorContext != "" {
		t.Fatal("retry retained prior attempt counters/errors")
	}
	_ = repo.FinishTask(ctx, task.ID, "failed", "OLD", "old error")
	_ = repo.FinishTask(ctx, task.ID, "done", "", "")
	got, _ = repo.GetSyncTask(ctx, task.ID)
	if got.ErrorCode != "" || got.ErrorContext != "" {
		t.Fatal("successful retry still shows failure")
	}
	importTask, _ := repo.CreateSyncTask(ctx, conn.ID, "selected", ScopeImport, false)
	_, _ = repo.TouchTask(ctx, importTask.ID, TaskProgress{Processed: 20, Created: 10})
	if err := repo.SetTaskProcessing(ctx, importTask.ID, 30); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetSyncTask(ctx, importTask.ID)
	if got.ProcessedCount != 20 || got.CreatedCount != 10 {
		t.Fatal("resuming an import erased saved progress")
	}
}

func TestSyncMixedStockResultsRemainVisibleAsPartial(t *testing.T) {
	s, repo, _, _ := newTestSyncService(t)
	ctx := context.Background()
	conn := mustConn(t, repo, nil, "mixed stock")
	task, _ := repo.CreateSyncTask(ctx, conn.ID, "full", ScopeCollect, false)
	up := &fakeUpstream{products: []adapter.Product{{ID: "A", Name: "A", Price: 100, IsActive: true, Stock: -2}, {ID: "B", Name: "B", Price: 100, IsActive: true, Stock: -2}}, total: 2, echo: true, stocks: map[string]int32{"A": 12}}
	if err := s.runLoop(ctx, task.ID, task, conn, up, loadScheduleSettings(conn), ScopeCollect, false, listOf(up)); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetSyncTask(ctx, task.ID)
	if got.ErrorCode != "PARTIAL_STOCK_FAILED" || !strings.Contains(got.ErrorContext, "库存成功 1，失败 1") {
		t.Fatalf("mixed stock result hidden: %+v", got)
	}
}

func TestSyncStockReadFailuresStopLaterQuotes(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		err        error
	}{
		{"rate limited", "RATE_LIMITED", adapter.ErrRateLimited},
		{"invalid credentials", "CREDENTIALS_INVALID", adapter.ErrCredentialsInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, _, _ := newTestSyncService(t)
			ctx := context.Background()
			conn := mustConn(t, repo, nil, tc.name)
			task, _ := repo.CreateSyncTask(ctx, conn.ID, "full", ScopeCollect, false)
			up := &isolatedQuoteUpstream{fakeUpstream: &fakeUpstream{products: []adapter.Product{{ID: "A", IsActive: true, Stock: -2}, {ID: "B", IsActive: true, Stock: -2}}, total: 2, echo: true}, stockErr: tc.err}
			cfg := loadScheduleSettings(conn)
			cfg.StockConc = 1
			if err := s.runLoop(ctx, task.ID, task, conn, up, cfg, ScopeCollect, false, listOf(up)); err != nil {
				t.Fatal(err)
			}
			got, _ := repo.GetSyncTask(ctx, task.ID)
			if got.Status != "failed" || got.ErrorCode != tc.code || up.stockReads != 1 || len(up.quoted) != 0 {
				t.Fatalf("reads continued after stock rejection: task=%+v stock=%d quotes=%v", got, up.stockReads, up.quoted)
			}
		})
	}
}
