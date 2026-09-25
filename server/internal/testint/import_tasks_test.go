//go:build integration

package testint

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplysynctask"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply"
)

func TestDurableImportTaskMySQL(t *testing.T) { testDurableImportTask(MySQL(t)) }
func TestDurableImportTaskPG(t *testing.T)    { testDurableImportTask(PG(t)) }
func testDurableImportTask(h *Harness) {
	t, ctx := h.T, context.Background()
	c := h.Data.Client
	conn := c.SupplyConnection.Create().SetName("durable import").SetDriver("zcard").SetBaseURL("https://example.com").SetCredentials([]byte("not-used")).SaveX(ctx)
	// Legacy tasks have NULL request keys; multiple old tasks must remain valid.
	for i := 0; i < 2; i++ {
		c.SupplySyncTask.Create().SetConnectionID(conn.ID).SetMode("full").SaveX(ctx)
	}
	payload := json.RawMessage(`{"tenant":0}`)
	task := c.SupplySyncTask.Create().SetConnectionID(conn.ID).SetMode("selected").SetScope("import").SetRequestKey("one-submit").SetImportPayload(payload).SetCancelRequestedAt(time.Now()).SaveX(ctx)
	if _, err := c.SupplySyncTask.Create().SetConnectionID(conn.ID).SetMode("selected").SetScope("import").SetRequestKey("one-submit").Save(ctx); err == nil {
		t.Fatal("duplicate request key accepted")
	}
	item := c.SupplyImportItem.Create().SetTaskID(task.ID).SetCode("P").SetName("库存待确认").SetSnapshot(json.RawMessage(`{"ID":"P","Price":1000}`)).SetSaved(true).SetStage("stock").SetState("retry").SetAttempts(2).SetNextAttemptAt(time.Now().Add(time.Minute).Unix()).SaveX(ctx)
	got := c.SupplyImportItem.GetX(ctx, item.ID)
	if !got.Saved || got.Stage != "stock" || got.Attempts != 2 || got.NextAttemptAt == 0 {
		t.Fatal("checkpoint was not persisted")
	}
	repo := supply.NewSupplyRepoImpl(h.Data, nil)
	runner := supply.NewSyncService(repo, nil, nil, nil, nil, nil, slog.Default())
	c.SupplyConnection.UpdateOneID(conn.ID).SetSyncLeaseToken("live").SetSyncLeaseUntil(time.Now().Add(time.Minute).Unix()).ExecX(ctx)
	if err := runner.RunSync(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if c.SupplySyncTask.GetX(ctx, task.ID).Status != supplysynctask.StatusPending {
		t.Fatal("active connection lease ignored")
	}
	c.SupplyConnection.UpdateOneID(conn.ID).SetSyncLeaseUntil(0).ExecX(ctx)
	if err := runner.RunSync(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if c.SupplySyncTask.GetX(ctx, task.ID).Status != supplysynctask.StatusCanceled {
		t.Fatal("recovered cancellation lost")
	}
	if c.SupplyConnection.GetX(ctx, conn.ID).SyncLeaseToken != "" {
		t.Fatal("lease was not released")
	}
	if got := c.SupplyImportItem.GetX(ctx, item.ID); !got.Saved || got.Attempts != 2 {
		t.Fatal("cancel destroyed successful checkpoint")
	}
}
