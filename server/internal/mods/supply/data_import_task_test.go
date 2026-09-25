package supply

import (
	"context"
	"encoding/json"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"errors"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyimportitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplysynctask"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/google/uuid"
)

type importAdapter struct {
	adapter.Adapter
	quotes, stocks int
	quote          func(context.Context, *adapter.Product) (*adapter.Product, error)
	stock          func(context.Context, string) (int32, error)
}

func (a *importAdapter) QuoteProduct(ctx context.Context, p *adapter.Product) (*adapter.Product, error) {
	a.quotes++
	if a.quote != nil {
		return a.quote(ctx, p)
	}
	out := *p
	out.FactoryPrice = p.Price
	return &out, nil
}
func (a *importAdapter) GetStock(ctx context.Context, code, sku string) (int32, error) {
	a.stocks++
	if a.stock != nil {
		return a.stock(ctx, code)
	}
	return 8, nil
}

func importFixture(t *testing.T) (*AdminSupplyService, *ent.SupplyConnection) {
	t.Helper()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "import")
	writer := catalog.NewProductRepoImpl(d, nil)
	svc := &SyncService{repo: repo, writer: writer, maintainer: writer, log: slog.Default()}
	return NewAdminSupplyService(repo, svc), conn
}
func enqueueTestImport(t *testing.T, s *AdminSupplyService, conn *ent.SupplyConnection, products []adapter.Product) (*ent.SupplySyncTask, *importPayload) {
	t.Helper()
	req := &adminv1.ImportProductsRequest{ConnectionId: conn.ID, RequestKey: uuid.NewString(), PricingMode: PriceModeEqual}
	byCode := map[string]adapter.Product{}
	for _, p := range products {
		req.Codes = append(req.Codes, p.ID)
		byCode[p.ID] = p
	}
	key, hash, err := importRequestIdentity(req)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.createImportTask(context.Background(), conn, req, byCode, key, hash)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := readImportPayload(context.Background(), task, false)
	if err != nil {
		t.Fatal(err)
	}
	return task, payload
}
func nextImportItem(t *testing.T, s *AdminSupplyService, id uint64) *ent.SupplyImportItem {
	t.Helper()
	return s.repo.entClient(context.Background()).SupplyImportItem.Query().Where(supplyimportitem.TaskID(id), supplyimportitem.StateIn("pending", "retry")).Order(supplyimportitem.ByID()).FirstX(context.Background())
}

// Existing pricing/reimport tests use the same durable item worker with a local
// adapter. This replaces their former synchronous HTTP result contract.
func runSelectedImportForTest(t *testing.T, s *AdminSupplyService, req *adminv1.ImportProductsRequest, products map[string]adapter.Product) (*adminv1.ImportProductsReply, error) {
	t.Helper()
	ctx := context.Background()
	req.RequestKey = uuid.NewString()
	conn, err := s.repo.GetConnection(ctx, req.ConnectionId)
	if err != nil {
		return nil, err
	}
	key, hash, err := importRequestIdentity(req)
	if err != nil {
		return nil, err
	}
	task, err := s.createImportTask(ctx, conn, req, products, key, hash)
	if err != nil {
		return nil, err
	}
	payload, _ := readImportPayload(ctx, task, false)
	conn, _ = s.repo.GetConnection(ctx, conn.ID)
	a := &importAdapter{}
	for {
		item, err := s.repo.entClient(ctx).SupplyImportItem.Query().Where(supplyimportitem.TaskID(task.ID), supplyimportitem.StateIn("pending", "retry")).First(ctx)
		if ent.IsNotFound(err) {
			break
		}
		if err != nil {
			return nil, err
		}
		if err := s.sync.attemptImportItem(ctx, task, payload, conn, a, item); err != nil {
			return nil, err
		}
	}
	result, err := s.importTaskProto(ctx, task)
	if err != nil {
		return nil, err
	}
	return &adminv1.ImportProductsReply{Imported: int32(result.Created), Updated: int32(result.Updated), Failed: int32(result.FailedCount), Task: result}, nil
}

func TestImportDurableStagesAndOnlyStockRetry(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	task, payload := enqueueTestImport(t, s, conn, []adapter.Product{{ID: "P", Name: "商品", Price: 1000, IsActive: true}})
	item := nextImportItem(t, s, task.ID)
	a := &importAdapter{}
	if err := s.sync.attemptImportItem(ctx, task, payload, conn, a, item); err != nil {
		t.Fatal(err)
	}
	saved := nextImportItem(t, s, task.ID)
	if !saved.Saved || saved.Stage != "stock" || saved.Attempts != 0 {
		t.Fatalf("checkpoint: %+v", saved)
	}
	p := s.repo.entClient(ctx).Product.GetX(ctx, saved.LocalProductID)
	if p.Status != 0 {
		t.Fatal("unknown stock was listed")
	}
	a.stock = func(context.Context, string) (int32, error) { return -2, context.DeadlineExceeded }
	err := s.sync.attemptImportItem(ctx, task, payload, conn, a, saved)
	if err == nil {
		t.Fatal("expected stock timeout")
	}
	if err := s.sync.failImportItem(ctx, saved, adapter.ClassifyImportError(err)); err != nil {
		t.Fatal(err)
	}
	progress, err := s.importTaskProto(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Created != 1 || progress.FailedCount != 0 || progress.StockPendingCount != 1 {
		t.Fatalf("double counted stock: %v", progress)
	}
	// Simulate a new process: reconstruct the service, discard all preview/cache state.
	writer := catalog.NewProductRepoImpl(s.repo.data, nil)
	resumed := &SyncService{repo: s.repo, writer: writer, log: slog.Default()}
	a.stock = nil
	if err := resumed.attemptImportItem(ctx, task, payload, conn, a, nextImportItem(t, s, task.ID)); err != nil {
		t.Fatal(err)
	}
	if a.quotes != 1 || a.stocks != 2 {
		t.Fatalf("stock retried pricing: quotes=%d stock=%d", a.quotes, a.stocks)
	}
	if p := s.repo.entClient(ctx).Product.GetX(ctx, saved.LocalProductID); p.Status != 1 || p.Price != 1000 {
		t.Fatalf("not activated: %+v", p)
	}
	if s.repo.entClient(ctx).Product.Query().CountX(ctx) != 1 {
		t.Fatal("duplicate after recovery")
	}
}

func TestImportCheckpointRollsBackProductOnFailure(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	task, payload := enqueueTestImport(t, s, conn, []adapter.Product{{ID: "P", Name: "商品", Price: 1000, IsActive: true}})
	if _, err := s.repo.data.DB.ExecContext(ctx, `CREATE TRIGGER fail_checkpoint BEFORE UPDATE ON supply_import_items WHEN NEW.saved = 1 BEGIN SELECT RAISE(ABORT, 'checkpoint unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.sync.attemptImportItem(ctx, task, payload, conn, &importAdapter{}, nextImportItem(t, s, task.ID)); err == nil {
		t.Fatal("expected checkpoint failure")
	}
	if s.repo.entClient(ctx).Product.Query().CountX(ctx) != 0 || s.repo.entClient(ctx).SupplyMapping.Query().CountX(ctx) != 0 {
		t.Fatal("product survived failed checkpoint")
	}
}

func TestImportSelectionIdempotencyAndTenant(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	p := adapter.Product{ID: "P", Name: "商品", Price: 1000, IsActive: true}
	req := &adminv1.ImportProductsRequest{ConnectionId: conn.ID, Codes: []string{"P", "P"}, RequestKey: "same-request"}
	key, hash, _ := importRequestIdentity(req)
	first, err := s.createImportTask(ctx, conn, req, map[string]adapter.Product{"P": p}, key, hash)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.createImportTask(ctx, conn, req, map[string]adapter.Product{"P": p}, key, hash)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || s.repo.entClient(ctx).SupplyImportItem.Query().CountX(ctx) != 1 {
		t.Fatal("duplicate submission")
	}
	req.MarkupAmountCents = 100
	_, different, _ := importRequestIdentity(req)
	if _, err := s.existingImport(ctx, conn.ID, key, different); err == nil {
		t.Fatal("changed request accepted")
	}
	other := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 42})
	if _, err := s.GetSyncTask(other, &adminv1.GetSyncTaskRequest{Id: first.ID}); err == nil {
		t.Fatal("cross tenant task read")
	}
	if _, err := s.ListImportItems(other, &adminv1.ListImportItemsRequest{Id: first.ID}); err == nil {
		t.Fatal("cross tenant items read")
	}
	if _, err := s.CancelSyncTask(other, &adminv1.CancelSyncTaskRequest{Id: first.ID}); err == nil {
		t.Fatal("cross tenant cancel")
	}
}

func TestImportLocksEditsAndConfigDuringQuote(t *testing.T) {
	for _, kind := range []string{"lock", "price", "config"} {
		t.Run(kind, func(t *testing.T) {
			s, conn := importFixture(t)
			ctx := context.Background()
			p := adapter.Product{ID: "P", Name: "商品", Price: 1000, FactoryPrice: 1000, IsActive: true, Stock: 8}
			if _, err := s.sync.ImportOne(ctx, conn, &p, nil, PriceModeEqual, 0, 0); err != nil {
				t.Fatal(err)
			}
			m, _ := s.repo.GetMapping(ctx, conn.ID, p.ID, "")
			task, payload := enqueueTestImport(t, s, conn, []adapter.Product{p})
			a := &importAdapter{quote: func(ctx context.Context, p *adapter.Product) (*adapter.Product, error) {
				switch kind {
				case "lock":
					s.repo.entClient(ctx).Product.UpdateOneID(m.LocalProductID).SetIsLocked(true).ExecX(ctx)
				case "price":
					s.repo.entClient(ctx).Product.UpdateOneID(m.LocalProductID).SetPrice(7777).ExecX(ctx)
				case "config":
					s.repo.entClient(ctx).SupplyConnection.UpdateOneID(conn.ID).SetPriceMarkupAmount(100).ExecX(ctx)
				}
				out := *p
				out.Price = 1500
				return &out, nil
			}}
			err := s.sync.attemptImportItem(ctx, task, payload, conn, a, nextImportItem(t, s, task.ID))
			if err == nil {
				t.Fatal("concurrent change overwritten")
			}
			if kind == "lock" && !data.IsProductLocked(err) {
				t.Fatal(err)
			}
			if kind == "price" && !errors.Is(err, errImportChanged) {
				t.Fatal(err)
			}
			if kind == "config" && !errors.Is(err, errImportConfiguration) {
				t.Fatal(err)
			}
			item := nextImportItem(t, s, task.ID)
			if item.Saved {
				t.Fatal("committed changed product")
			}
		})
	}
}

func TestImportLargeSelectionHasIndependentQuoteBudgets(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	products := make([]adapter.Product, 1168)
	for i := range products {
		products[i] = adapter.Product{ID: fmt.Sprint(i), Name: fmt.Sprint(i), Price: 1000, IsActive: true}
	}
	task, payload := enqueueTestImport(t, s, conn, products)
	var firstDeadline time.Time
	a := &importAdapter{quote: func(ctx context.Context, p *adapter.Product) (*adapter.Product, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 80*time.Second {
			t.Fatal("shared/short quote deadline")
		}
		if firstDeadline.IsZero() {
			firstDeadline = deadline
		} else if !deadline.After(firstDeadline) {
			t.Fatal("quote deadline did not renew")
		}
		out := *p
		return &out, nil
	}}
	rows := s.repo.entClient(ctx).SupplyImportItem.Query().Where(supplyimportitem.TaskID(task.ID)).AllX(ctx)
	for _, item := range rows {
		if err := s.sync.attemptImportItem(ctx, task, payload, conn, a, item); err != nil {
			t.Fatalf("%s: %v", item.Code, err)
		}
	}
	progress, err := s.importTaskProto(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Created != 1168 || progress.FailedCount != 0 || progress.StockPendingCount != 1168 || a.quotes != 1168 {
		t.Fatalf("large import: %v", progress)
	}
}

func TestImportRetryClassificationAndBoundedAttempts(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	task, _ := enqueueTestImport(t, s, conn, []adapter.Product{{ID: "P", Name: "商品"}})
	item := nextImportItem(t, s, task.ID)
	for n := 1; n <= 3; n++ {
		item.Attempts = n
		if err := s.sync.failImportItem(ctx, item, adapter.ClassifyImportError(context.DeadlineExceeded)); err != nil {
			t.Fatal(err)
		}
	}
	got := s.repo.entClient(ctx).SupplyImportItem.GetX(ctx, item.ID)
	if got.State != "failed" || got.NextAttemptAt != 0 {
		t.Fatal("unbounded retries")
	}
}

func TestImportExpiredWorkerCannotCommit(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	task, payload := enqueueTestImport(t, s, conn, []adapter.Product{{ID: "P", Name: "商品", Price: 1000, IsActive: true}})
	s.repo.entClient(ctx).SupplyConnection.UpdateOneID(conn.ID).SetSyncLeaseToken("new-worker").SetSyncLeaseUntil(time.Now().Add(time.Minute).Unix()).ExecX(ctx)
	stale := context.WithValue(ctx, taskLeaseKey{}, taskLease{conn.ID, "old-worker"})
	if err := s.sync.attemptImportItem(stale, task, payload, conn, &importAdapter{}, nextImportItem(t, s, task.ID)); !errors.Is(err, errTaskLeaseLost) {
		t.Fatal(err)
	}
	if s.repo.entClient(ctx).Product.Query().CountX(ctx) != 0 {
		t.Fatal("late worker wrote product")
	}
}

func TestImportStopAndAuthDoNotFailRemainingProducts(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	task, payload := enqueueTestImport(t, s, conn, []adapter.Product{{ID: "1", Name: "一"}, {ID: "2", Name: "二"}})
	s.repo.entClient(ctx).SupplySyncTask.UpdateOneID(task.ID).SetCancelRequestedAt(time.Now()).ExecX(ctx)
	if err := s.sync.executeImportItems(ctx, task, payload, conn, &importAdapter{}); err != nil {
		t.Fatal(err)
	}
	got := s.repo.entClient(ctx).SupplySyncTask.GetX(ctx, task.ID)
	if got.Status != supplysynctask.StatusCanceled {
		t.Fatal(got.Status)
	}
	raw, _ := json.Marshal(payload)
	if len(raw) == 0 {
		t.Fatal("missing durable configuration")
	}
	progress, _ := s.importTaskProto(ctx, got)
	if progress.PendingCount != 2 || progress.FailedCount != 0 {
		t.Fatal(progress)
	}
}

func TestImportRecoveryWithoutRedisClaimsExpiredLease(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	task, _ := enqueueTestImport(t, s, conn, []adapter.Product{{ID: "P", Name: "商品", Price: 1000, IsActive: true}})
	c := s.repo.entClient(ctx)
	c.SupplySyncTask.UpdateOneID(task.ID).SetStatus(supplysynctask.StatusProcessing).SetCancelRequestedAt(time.Now()).ExecX(ctx)
	c.SupplyConnection.UpdateOneID(conn.ID).SetSyncTaskID(task.ID).SetSyncLeaseToken("dead-process").SetSyncLeaseUntil(time.Now().Add(-time.Minute).Unix()).ExecX(ctx)
	// A newly constructed executor shares only the database with the dead worker.
	recovered := &SyncService{repo: s.repo, writer: s.sync.writer, log: slog.Default()}
	recovered.ResumeTasks(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got := c.SupplySyncTask.GetX(ctx, task.ID)
		current := c.SupplyConnection.GetX(ctx, conn.ID)
		if got.Status == supplysynctask.StatusCanceled && current.SyncLeaseToken == "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("restart recovery lost pending cancellation or did not release lease")
}

func TestImportActiveLeaseExcludesAnotherTask(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	task, _ := enqueueTestImport(t, s, conn, []adapter.Product{{ID: "P", Name: "商品"}})
	c := s.repo.entClient(ctx)
	c.SupplyConnection.UpdateOneID(conn.ID).SetSyncTaskID(999).SetSyncLeaseToken("busy").SetSyncLeaseUntil(time.Now().Add(time.Minute).Unix()).ExecX(ctx)
	if err := s.sync.RunSync(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if c.SupplySyncTask.GetX(ctx, task.ID).Status != supplysynctask.StatusPending {
		t.Fatal("competing task acquired active lease")
	}
	if c.SupplyImportItem.Query().OnlyX(ctx).Attempts != 0 {
		t.Fatal("competing task called upstream")
	}
}

func TestImportStockRecoveryPreservesManualUnlisting(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	task, payload := enqueueTestImport(t, s, conn, []adapter.Product{{ID: "P", Name: "商品", Price: 1000, IsActive: true}})
	a := &importAdapter{}
	if err := s.sync.attemptImportItem(ctx, task, payload, conn, a, nextImportItem(t, s, task.ID)); err != nil {
		t.Fatal(err)
	}
	item := nextImportItem(t, s, task.ID)
	s.repo.entClient(ctx).Product.UpdateOneID(item.LocalProductID).SetName("人工编辑").SetStatus(0).ExecX(ctx)
	if err := s.sync.attemptImportItem(ctx, task, payload, conn, a, item); !errors.Is(err, errImportChanged) {
		t.Fatal(err)
	}
	if s.repo.entClient(ctx).Product.GetX(ctx, item.LocalProductID).Status != 0 {
		t.Fatal("stock completion undid operator edit")
	}
}

func TestImportSQLiteCheckpointSurvivesDatabaseReopen(t *testing.T) {
	ctx := context.Background()
	source := "file:" + filepath.Join(t.TempDir(), "import.sqlite") + "?_pragma=foreign_keys(1)"
	open := func() *AdminSupplyService {
		handle, err := db.SQLite.Open(source)
		if err != nil {
			t.Fatal(err)
		}
		client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
		if err := client.Schema.Create(ctx); err != nil {
			t.Fatal(err)
		}
		d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
		repo := NewSupplyRepoImpl(d, newTestBox(t))
		writer := catalog.NewProductRepoImpl(d, nil)
		return NewAdminSupplyService(repo, &SyncService{repo: repo, writer: writer, log: slog.Default()})
	}
	first := open()
	conn := mustConn(t, first.repo, first.repo.data, "reopen")
	task, payload := enqueueTestImport(t, first, conn, []adapter.Product{{ID: "P", Name: "商品", Price: 1000, IsActive: true}})
	if err := first.sync.attemptImportItem(ctx, task, payload, conn, &importAdapter{}, nextImportItem(t, first, task.ID)); err != nil {
		t.Fatal(err)
	}
	first.repo.data.Client.Close()
	second := open()
	defer second.repo.data.Client.Close()
	task = second.repo.entClient(ctx).SupplySyncTask.GetX(ctx, task.ID)
	payload, err := readImportPayload(ctx, task, false)
	if err != nil {
		t.Fatal(err)
	}
	item := nextImportItem(t, second, task.ID)
	a := &importAdapter{}
	if !item.Saved || item.Stage != "stock" {
		t.Fatal("durable checkpoint lost on reopen")
	}
	if err := second.sync.attemptImportItem(ctx, task, payload, conn, a, item); err != nil {
		t.Fatal(err)
	}
	if a.quotes != 0 || a.stocks != 1 || second.repo.entClient(ctx).Product.Query().CountX(ctx) != 1 {
		t.Fatal("recovery repeated import")
	}
}

func TestImportRetryScopeAndChangedConfiguration(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	c := s.repo.entClient(ctx)
	task, _ := enqueueTestImport(t, s, conn, []adapter.Product{{ID: "done", Name: "完成"}, {ID: "stock", Name: "库存"}, {ID: "failed", Name: "失败"}, {ID: "pending", Name: "未处理"}})
	c.SupplyImportItem.Update().Where(supplyimportitem.TaskID(task.ID), supplyimportitem.CodeEQ("done")).SetSaved(true).SetState("done").SetStage("stock").SetAttempts(1).ExecX(ctx)
	c.SupplyImportItem.Update().Where(supplyimportitem.TaskID(task.ID), supplyimportitem.CodeEQ("stock")).SetSaved(true).SetState("failed").SetStage("stock").SetAttempts(3).ExecX(ctx)
	c.SupplyImportItem.Update().Where(supplyimportitem.TaskID(task.ID), supplyimportitem.CodeEQ("failed")).SetState("failed").SetAttempts(3).ExecX(ctx)
	c.SupplySyncTask.UpdateOneID(task.ID).SetStatus(supplysynctask.StatusDone).ExecX(ctx)
	if _, err := s.prepareImportRetry(ctx, &adminv1.RetryImportTaskRequest{Id: task.ID, Scope: "stock"}); err != nil {
		t.Fatal(err)
	}
	rows := c.SupplyImportItem.Query().Where(supplyimportitem.TaskID(task.ID)).AllX(ctx)
	for _, r := range rows {
		if r.Code == "stock" && (r.State != "pending" || r.Attempts != 0 || !r.Saved || r.Stage != "stock") {
			t.Fatal("stock retry repeated import")
		}
		if r.Code == "failed" && (r.State != "failed" || r.Attempts != 3) {
			t.Fatal("stock retry changed unrelated failure")
		}
		if r.Code == "done" && (r.State != "done" || r.Attempts != 1) {
			t.Fatal("stock retry reset completed product")
		}
	}
	c.SupplySyncTask.UpdateOneID(task.ID).SetStatus(supplysynctask.StatusFailed).ExecX(ctx)
	c.SupplyConnection.UpdateOneID(conn.ID).SetPriceMarkupAmount(100).ExecX(ctx)
	if _, err := s.prepareImportRetry(ctx, &adminv1.RetryImportTaskRequest{Id: task.ID, Scope: "remaining"}); err == nil {
		t.Fatal("config change silently accepted")
	}
	if _, err := s.prepareImportRetry(ctx, &adminv1.RetryImportTaskRequest{Id: task.ID, Scope: "remaining", ConfirmConfig: true}); err != nil {
		t.Fatal(err)
	}
	if c.SupplyImportItem.Query().Where(supplyimportitem.TaskID(task.ID), supplyimportitem.CodeEQ("done")).OnlyX(ctx).State != "done" {
		t.Fatal("confirmed config reset successful product")
	}
}

func TestImportTaskPreventsDeletingConnection(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	enqueueTestImport(t, s, conn, []adapter.Product{{ID: "P", Name: "商品"}})
	if err := s.repo.DeleteConnection(ctx, conn.ID); err == nil {
		t.Fatal("deleted connection with pending import")
	}
}

func TestImportStockRetryDoesNotResumeCanceledImports(t *testing.T) {
	s, conn := importFixture(t)
	ctx := context.Background()
	c := s.repo.entClient(ctx)
	task, payload := enqueueTestImport(t, s, conn, []adapter.Product{{ID: "stock", Name: "库存", Price: 1000, IsActive: true}, {ID: "untouched", Name: "未处理", Price: 1000, IsActive: true}})
	first := nextImportItem(t, s, task.ID)
	if err := s.sync.attemptImportItem(ctx, task, payload, conn, &importAdapter{}, first); err != nil {
		t.Fatal(err)
	}
	c.SupplyImportItem.UpdateOneID(first.ID).SetState("failed").SetAttempts(3).ExecX(ctx)
	c.SupplySyncTask.UpdateOneID(task.ID).SetStatus(supplysynctask.StatusCanceled).ExecX(ctx)
	task, err := s.prepareImportRetry(ctx, &adminv1.RetryImportTaskRequest{Id: task.ID, Scope: "stock"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err = readImportPayload(ctx, task, false)
	if err != nil {
		t.Fatal(err)
	}
	a := &importAdapter{}
	if err := s.sync.executeImportItems(ctx, task, payload, conn, a); err != nil {
		t.Fatal(err)
	}
	if a.quotes != 0 || a.stocks != 1 {
		t.Fatalf("unexpected upstream requests: quote=%d stock=%d", a.quotes, a.stocks)
	}
	remaining := c.SupplyImportItem.Query().Where(supplyimportitem.TaskID(task.ID), supplyimportitem.CodeEQ("untouched")).OnlyX(ctx)
	if remaining.State != "pending" || remaining.Saved {
		t.Fatal("stock retry imported unrelated product")
	}
	if c.SupplySyncTask.GetX(ctx, task.ID).Status != supplysynctask.StatusCanceled {
		t.Fatal("unprocessed selection falsely completed")
	}
}
