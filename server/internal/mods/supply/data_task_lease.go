package supply

import (
	"context"
	"errors"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplysynctask"
	"github.com/google/uuid"
)

const ScopeImport = "import"
const taskLeaseSeconds = 120

var errTaskLeaseLost = errors.New("货源任务执行权已失效")

type taskLeaseKey struct{}
type taskLease struct {
	connectionID uint64
	token        string
}

// All maintenance scopes share one connection lease, including installations
// without Redis. The token fences late workers after a crash/lease takeover.
func (s *SyncService) RunSync(ctx context.Context, taskID uint64) error {
	task, err := s.repo.GetSyncTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Status != supplysynctask.StatusPending && !(task.Scope == ScopeImport && task.Status == supplysynctask.StatusProcessing) {
		return nil
	}
	token := uuid.NewString()
	n, err := s.repo.entClient(ctx).SupplyConnection.Update().Where(
		supplyconnection.ID(task.ConnectionID), supplyconnection.SyncLeaseUntilLTE(time.Now().Unix()),
	).SetSyncTaskID(taskID).SetSyncLeaseToken(token).SetSyncLeaseUntil(time.Now().Unix() + taskLeaseSeconds).Save(ctx)
	if err != nil || n == 0 {
		return err
	}
	lease := taskLease{task.ConnectionID, token}
	runCtx, cancel := context.WithCancel(context.WithValue(ctx, taskLeaseKey{}, lease))
	heartbeatDone := make(chan struct{})
	defer func() {
		cancel()
		<-heartbeatDone
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		_, _ = s.repo.entClient(cleanup).SupplyConnection.Update().Where(supplyconnection.ID(lease.connectionID), supplyconnection.SyncLeaseToken(token)).SetSyncLeaseUntil(0).SetSyncLeaseToken("").SetSyncTaskID(0).Save(cleanup)
	}()
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				count, e := s.repo.entClient(runCtx).SupplyConnection.Update().Where(supplyconnection.ID(lease.connectionID), supplyconnection.SyncLeaseToken(token), supplyconnection.SyncLeaseUntilGT(time.Now().Unix())).SetSyncLeaseUntil(time.Now().Unix() + taskLeaseSeconds).Save(runCtx)
				if e != nil || count != 1 {
					cancel()
					return
				}
			}
		}
	}()
	// Reload after acquiring: another delivery may have completed this task.
	task, err = s.repo.GetSyncTask(runCtx, taskID)
	if err != nil {
		return err
	}
	if task.Scope == ScopeImport {
		if task.Status != supplysynctask.StatusPending && task.Status != supplysynctask.StatusProcessing {
			return nil
		}
		return s.runImportTask(runCtx, task)
	}
	return s.runSync(runCtx, taskID)
}

func (s *SyncService) guardTaskLease(ctx context.Context) error {
	lease, ok := ctx.Value(taskLeaseKey{}).(taskLease)
	if !ok {
		return nil
	} // synchronous callers/tests still use the product guard
	// Caller holds a data.Tx. A no-op write takes the row lock even when MySQL
	// reports zero changed rows (updated_at has millisecond precision).
	c := s.repo.entClient(ctx)
	if err := c.SupplyConnection.UpdateOneID(lease.connectionID).AddRetryMax(0).Exec(ctx); err != nil {
		return err
	}
	current, err := c.SupplyConnection.Get(ctx, lease.connectionID)
	if err != nil {
		return err
	}
	if current.SyncLeaseToken != lease.token || current.SyncLeaseUntil <= time.Now().Unix() {
		return errTaskLeaseLost
	}
	return nil
}

// The database, not an in-memory goroutine or Redis delivery, owns pending work.
func (s *SyncService) ResumeTasks(ctx context.Context) {
	tasks, err := s.repo.entClient(ctx).SupplySyncTask.Query().Where(supplysynctask.Or(
		supplysynctask.StatusEQ(supplysynctask.StatusPending),
		supplysynctask.And(supplysynctask.ScopeEQ(ScopeImport), supplysynctask.StatusEQ(supplysynctask.StatusProcessing)),
	)).Order(supplysynctask.ByID()).All(ctx)
	if err != nil {
		s.log.Warn("supply.tasks.recovery_failed", "err", err)
		return
	}
	seen := map[uint64]bool{}
	for _, task := range tasks {
		if seen[task.ConnectionID] {
			continue
		}
		seen[task.ConnectionID] = true
		conn, err := s.repo.GetConnection(ctx, task.ConnectionID)
		if err != nil || conn.SyncLeaseUntil > time.Now().Unix() {
			continue
		}
		if err := s.StartTask(ctx, task.ID); err != nil {
			s.log.Warn("supply.tasks.dispatch_failed", "task_id", task.ID, "err", err)
		}
	}
}
