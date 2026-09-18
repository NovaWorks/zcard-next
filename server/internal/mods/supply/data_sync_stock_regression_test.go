package supply

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecheckAllStockFailuresReportedDone(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _ := newTestSyncService(t)
	conn := mustConn(t, repo, nil, "stock-failure-audit")
	task, err := repo.CreateSyncTask(ctx, conn.ID, "full", ScopeCollect, false)
	if err != nil {
		t.Fatal(err)
	}
	up := &fakeUpstream{products: []adapter.Product{{ID: "A", Name: "A", IsActive: true, Stock: -2}, {ID: "B", Name: "B", IsActive: true, Stock: -2}}, total: 2, echo: true}
	if err := svc.runLoop(ctx, task.ID, task, conn, up, loadScheduleSettings(conn), ScopeCollect, false, listOf(up)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetSyncTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, err := repo.GetMapping(ctx, conn.ID, "A", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := repo.GetMapping(ctx, conn.ID, "B", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("all stock lookups failed: status=%s error_code=%q error_context=%q stocks=[%d,%d]", got.Status, got.ErrorCode, got.ErrorContext, a.UpStock, b.UpStock)
	if got.Status == "done" && got.ErrorCode == "" && got.ErrorContext == "" {
		t.Fatal("all stock queries failed but task reports a clean completion")
	}
}

func TestStockOnlyRecoveryPreservesCatalogAndSkipsHealthy(t *testing.T) {
	svc, repo, _, _ := newTestSyncService(t)
	ctx := context.Background()
	conn := mustConn(t, repo, nil, "recovery")
	client := repo.entClient(ctx)
	for _, name := range []string{"failed", "healthy", "archived"} {
		status := int8(1)
		if name == "archived" {
			status = -1
		}
		p := client.Product.Create().SetName(name).SetSlug(name).SetStatus(status).SetPrice(1234).SetUpstreamSourceID(conn.ID).SetUpstreamProductCode(name).SaveX(ctx)
		stock := int32(-2)
		if name == "healthy" {
			stock = 99
		}
		client.SupplyMapping.Create().SetConnectionID(conn.ID).SetUpstreamProduct(name).SetLocalProductID(p.ID).SetUpStock(stock).SetStockCheckedAt(time.Now().Add(-time.Minute)).SaveX(ctx)
	}
	task, err := repo.CreateSyncTask(ctx, conn.ID, "failed", ScopeStock, false)
	if err != nil {
		t.Fatal(err)
	}
	up := &fakeUpstream{stocks: map[string]int32{"failed": 37, "healthy": 1, "archived": 5}}
	if err := svc.runStockOnly(ctx, task, conn, up, loadScheduleSettings(conn)); err != nil {
		t.Fatal(err)
	}
	for _, p := range client.Product.Query().AllX(ctx) {
		if p.Price != 1234 || (p.Name == "archived" && p.Status != -1) {
			t.Fatal("catalog changed", p)
		}
		m, _ := repo.GetMapping(ctx, conn.ID, p.Name, "")
		want := int32(37)
		if p.Name == "healthy" {
			want = 99
		}
		if p.Name == "archived" {
			want = -2
		}
		if m.UpStock != want {
			t.Fatalf("%s stock=%d task=%+v mapping=%+v product=%+v", p.Name, m.UpStock, client.SupplySyncTask.GetX(ctx, task.ID), m, p)
		}
	}
	got, _ := repo.GetSyncTask(ctx, task.ID)
	if got.Status != "done" || got.ProcessedCount != 1 {
		t.Fatal(got)
	}
}

type limitedStockUpstream struct {
	*fakeUpstream
	calls atomic.Int32
}

func (a *limitedStockUpstream) GetStock(context.Context, string, string) (int32, error) {
	a.calls.Add(1)
	return -2, adapter.ErrRateLimited
}
func TestStockBackfillStopsAfterLimitedBatch(t *testing.T) {
	svc, _, _, _ := newTestSyncService(t)
	a := &limitedStockUpstream{fakeUpstream: &fakeUpstream{}}
	items := make([]adapter.Product, 20)
	for i := range items {
		items[i] = adapter.Product{ID: fmt.Sprint(i), Stock: -2}
	}
	if err := svc.backfillStocks(context.Background(), a, scheduleSettings{StockConc: 2}, items, 0); err != nil {
		t.Fatal(err)
	}
	if a.calls.Load() != 2 {
		t.Fatalf("continued after rate limit: %d", a.calls.Load())
	}
	for _, p := range items {
		if p.Stock != -2 || p.StockError == "" || p.StockCheckedAt.IsZero() {
			t.Fatalf("unreported stock failure: %+v", p)
		}
	}
}
