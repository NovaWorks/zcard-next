package supply

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplysynctask"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

func TestCatalogSkippedItemIsNotShelved(t *testing.T) {
	s, r, _, _ := newTestSyncService(t)
	ctx := context.Background()
	conn := mustConn(t, r, nil, "partial")
	c := r.entClient(ctx)
	a := c.Product.Create().SetName("A").SetSlug("partial-a").SetPrice(123).SetStatus(1).SetAutoListing(true).SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("A").SaveX(ctx)
	task, err := r.CreateSyncTask(ctx, conn.ID, "full", ScopeStatus, false)
	if err != nil {
		t.Fatal(err)
	}
	up := &fakeUpstream{}
	list := func(context.Context, int, int) (*adapter.ProductList, error) {
		return &adapter.ProductList{Total: 2, IncludesInactive: true, SkippedIDs: []string{"A"}, Items: []adapter.Product{{ID: "B", IsActive: true, Stock: 5}}}, nil
	}
	if err := s.runLoop(ctx, task.ID, task, conn, up, loadScheduleSettings(conn), ScopeStatus, false, list); err != nil {
		t.Fatal(err)
	}
	got := c.Product.GetX(ctx, a.ID)
	result := c.SupplySyncTask.GetX(ctx, task.ID)
	if got.Status != 1 || got.Price != 123 || result.ErrorCode != "PARTIAL_SYNC_FAILED" || !strings.Contains(result.ErrorContext, "A") {
		t.Fatal("skipped item lost state or failure was hidden", got, result)
	}
}

func TestSyncRetrySurvivesFinishedWorkerContext(t *testing.T) {
	s, r, _, _ := newTestSyncService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := mustConn(t, r, nil, "retry-context")
	r.entClient(ctx).SupplyConnection.UpdateOneID(conn.ID).SetDriver("invalid-driver").ExecX(ctx)
	task, err := r.CreateSyncTask(ctx, conn.ID, "full", ScopeStatus, false)
	if err != nil {
		t.Fatal(err)
	}
	old := syncRetryIntervals
	syncRetryIntervals = []time.Duration{5 * time.Millisecond}
	defer func() { syncRetryIntervals = old; clearSyncRetry(task.ID) }()
	reset := make(chan struct{}, 1)
	r.entClient(ctx).SupplySyncTask.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			v, err := next.Mutate(ctx, m)
			if err == nil {
				if status, ok := m.(*ent.SupplySyncTaskMutation).Status(); ok && status == supplysynctask.StatusPending {
					select {
					case reset <- struct{}{}:
					default:
					}
				}
			}
			return v, err
		})
	})
	s.failAndMaybeRetry(ctx, task.ID, conn.ID, "LIST_PRODUCTS_FAILED", "temporary")
	cancel()
	select {
	case <-reset:
	case <-time.After(time.Second):
		t.Fatal("worker cancellation discarded scheduled retry")
	}
	// Wait for the detached retry to finish before restoring globals and closing DB.
	for until := time.Now().Add(time.Second); time.Now().Before(until); time.Sleep(time.Millisecond) {
		task, err = r.GetSyncTask(context.Background(), task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == supplysynctask.StatusFailed && task.ErrorCode != "LIST_PRODUCTS_FAILED" {
			return
		}
	}
	t.Fatal("retry did not finish")
}
