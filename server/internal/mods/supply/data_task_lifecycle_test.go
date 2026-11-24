package supply

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPacedMaintenanceGetsSameBudgetWithRedis(t *testing.T) {
	s, repo, _, _ := newTestSyncService(t)
	ctx := context.Background()
	conn := mustConn(t, repo, nil, "paced-budget")
	task, _ := repo.CreateSyncTask(ctx, conn.ID, "full", ScopePrice, false)
	q := &listingQueue{}
	s.enq = q
	if err := s.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if len(q.tasks) != 1 || q.tasks[0].Timeout != supplyTaskTimeout || q.tasks[0].Timeout <= 30*time.Minute {
		t.Fatalf("paced catalog still has a short queue deadline: %+v", q.tasks)
	}
}

func TestLongCatalogHeartbeatPreservesProgressAndHonorsCancel(t *testing.T) {
	s, repo, _, _ := newTestSyncService(t)
	ctx := context.Background()
	conn := mustConn(t, repo, nil, "catalog-heartbeat")
	task, _ := repo.CreateSyncTask(ctx, conn.ID, "full", ScopeCollect, false)
	if err := repo.SetTaskProcessing(ctx, task.ID, 3); err != nil {
		t.Fatal(err)
	}
	_, _ = repo.TouchTask(ctx, task.ID, TaskProgress{Processed: 2, Created: 1})
	c := repo.entClient(ctx)
	c.SupplySyncTask.UpdateOneID(task.ID).SetHeartbeatAt(time.Now().Add(-time.Hour)).ExecX(ctx)
	c.SupplyConnection.UpdateOneID(conn.ID).SetSyncLeaseToken("owner").SetSyncLeaseUntil(time.Now().Unix() + taskLeaseSeconds).ExecX(ctx)
	lease := taskLease{conn.ID, "owner"}
	if cancel, err := s.heartbeatTask(ctx, lease, task.ID); err != nil || cancel {
		t.Fatalf("heartbeat: canceled=%v err=%v", cancel, err)
	}
	stale, err := repo.ListStaleProcessing(ctx, time.Now().Add(-10*time.Minute))
	got, _ := repo.GetSyncTask(ctx, task.ID)
	if err != nil || len(stale) != 0 || got.ProcessedCount != 2 || got.CreatedCount != 1 {
		t.Fatalf("long read lost heartbeat or progress: task=%+v stale=%v err=%v", got, stale, err)
	}
	c.SupplySyncTask.UpdateOneID(task.ID).SetCancelRequestedAt(time.Now()).ExecX(ctx)
	if cancel, err := s.heartbeatTask(ctx, lease, task.ID); err != nil || !cancel {
		t.Fatalf("long catalog ignored cancellation: canceled=%v err=%v", cancel, err)
	}
	c.SupplyConnection.UpdateOneID(conn.ID).SetSyncLeaseToken("replacement").ExecX(ctx)
	if _, err := s.heartbeatTask(ctx, lease, task.ID); !errors.Is(err, errTaskLeaseLost) {
		t.Fatalf("stale worker retained execution: %v", err)
	}
}
