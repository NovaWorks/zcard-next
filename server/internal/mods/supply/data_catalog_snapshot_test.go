package supply

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

type catalogTestAdapter struct {
	adapter.Adapter
	calls    atomic.Int32
	entered  chan struct{}
	release  chan struct{}
	fail     error
	deadline atomic.Int64
}

func (a *catalogTestAdapter) ListProducts(ctx context.Context, page, size int, inactive bool) (*adapter.ProductList, error) {
	a.calls.Add(1)
	if a.entered != nil {
		select {
		case a.entered <- struct{}{}:
		default:
		}
	}
	if a.release != nil {
		select {
		case <-a.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if d, ok := ctx.Deadline(); ok {
		a.deadline.Store(int64(time.Until(d)))
	}
	if a.fail != nil {
		return nil, a.fail
	}
	return &adapter.ProductList{Items: []adapter.Product{{ID: "p", Name: "商品", CategoryID: "c", Price: 100}}, Categories: []adapter.Category{{ID: "c", Name: "分类"}}}, nil
}
func attachCatalogAdapter(s *AdminSupplyService, a adapter.Adapter) {
	s.adapterFactory = func(string, string, adapter.Credentials, []int) (adapter.Adapter, error) { return a, nil }
}
func catalogFixture(t *testing.T) (*AdminSupplyService, *ent.SupplyConnection, *catalogTestAdapter) {
	t.Helper()
	r, d := newTestRepo(t)
	c := mustConn(t, r, d, "catalog")
	s := NewAdminSupplyService(r, nil)
	a := &catalogTestAdapter{}
	attachCatalogAdapter(s, a)
	return s, c, a
}
func awaitCatalog(t *testing.T, s *AdminSupplyService, id uint64, status string) *ent.SupplyCatalogSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, e := s.repo.entClient(context.Background()).SupplyCatalogSnapshot.Get(context.Background(), id)
		if e == nil && r.Status == status {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("catalog state not reached", status)
	return nil
}
func TestCatalogJobSurvivesRequestAndSingleFlight(t *testing.T) {
	s, c, a := catalogFixture(t)
	a.entered = make(chan struct{}, 1)
	a.release = make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	reply, err := s.PreviewProducts(ctx, &adminv1.PreviewProductsRequest{ConnectionId: c.ID, Async: true})
	if err != nil || reply.SnapshotId == "" || reply.Status != "pending" {
		t.Fatalf("start: %v %v", reply, err)
	}
	<-a.entered
	cancel()
	row, err := s.snapshotRow(context.Background(), c, reply.SnapshotId)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.runCatalogSnapshot(row.ID) }()
	}
	wg.Wait()
	// A second browser opening the same channel joins the existing job.
	same, err := s.ensureCatalogSnapshot(context.Background(), c.ID, true)
	if err != nil || same.ID != row.ID {
		t.Fatalf("duplicate job: %v", err)
	}
	close(a.release)
	awaitCatalog(t, s, row.ID, "ready")
	if a.calls.Load() != 1 || time.Duration(a.deadline.Load()) < 4*time.Minute {
		t.Fatalf("calls=%d deadline=%v", a.calls.Load(), time.Duration(a.deadline.Load()))
	}
	// New service instance and cleared memory cache must read the durable snapshot.
	other := NewAdminSupplyService(s.repo, nil)
	attachCatalogAdapter(other, a)
	previewCache.Lock()
	delete(previewCache.m, c.ID)
	previewCache.Unlock()
	got, err := other.PreviewProducts(context.Background(), &adminv1.PreviewProductsRequest{ConnectionId: c.ID, SnapshotId: reply.SnapshotId})
	if err != nil || got.Total != 1 || got.Status != "ready" || a.calls.Load() != 1 {
		t.Fatalf("snapshot read: %v %v", got, err)
	}
	entry, _, err := other.readCatalogSnapshot(context.Background(), c, reply.SnapshotId)
	if err != nil || entry.byCode["p"].Name != "商品" {
		t.Fatal("cannot reuse selection", err)
	}
}
func TestCatalogSnapshotBoundariesAndRefresh(t *testing.T) {
	s, c, a := catalogFixture(t)
	ctx := context.Background()
	first, e := s.ensureCatalogSnapshot(ctx, c.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	s.runCatalogSnapshot(first.ID)
	second, e := s.ensureCatalogSnapshot(ctx, c.ID, true)
	if e != nil || second.ID == first.ID {
		t.Fatal("refresh did not create immutable version", e)
	}
	if _, _, e = s.readCatalogSnapshot(ctx, c, first.Token); e != nil {
		t.Fatal("refresh invalidated first buyer selection", e)
	}
	foreign := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 9})
	if _, _, e = s.readCatalogSnapshot(foreign, c, first.Token); e == nil {
		t.Fatal("cross-tenant snapshot exposed")
	}
	c2 := *c
	c2.ID++
	if _, _, e = s.readCatalogSnapshot(ctx, &c2, first.Token); e == nil {
		t.Fatal("cross-channel snapshot exposed")
	}
	c2 = *c
	c2.Credentials = []byte("changed")
	if _, _, e = s.readCatalogSnapshot(ctx, &c2, first.Token); e == nil {
		t.Fatal("changed credentials accepted")
	}
	s.repo.entClient(ctx).SupplyCatalogSnapshot.UpdateOneID(first.ID).SetExpiresAt(time.Now().Add(-time.Second).Unix()).ExecX(ctx)
	if _, _, e = s.readCatalogSnapshot(ctx, c, first.Token); e == nil {
		t.Fatal("expired snapshot accepted")
	}
	if a.calls.Load() != 1 {
		t.Fatal("reads unexpectedly fetched upstream")
	}
}
func TestCatalogFailedJobRequiresExplicitRetryAndLeaseRecovery(t *testing.T) {
	s, c, a := catalogFixture(t)
	ctx := context.Background()
	a.fail = context.DeadlineExceeded
	row, e := s.ensureCatalogSnapshot(ctx, c.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	s.runCatalogSnapshot(row.ID)
	failed := awaitCatalog(t, s, row.ID, "failed")
	if !strings.Contains(failed.Message, "120 秒") || strings.Contains(failed.Message, "context deadline") {
		t.Fatal(failed.Message)
	}
	same, e := s.ensureCatalogSnapshot(ctx, c.ID, false)
	if e != nil || same.ID != row.ID {
		t.Fatal("failed job retried silently", e)
	}
	retry, e := s.ensureCatalogSnapshot(ctx, c.ID, true)
	if e != nil || retry.ID == row.ID {
		t.Fatal("explicit retry failed", e)
	}
	a.fail = nil
	s.repo.entClient(ctx).SupplyCatalogSnapshot.UpdateOneID(retry.ID).SetStatus("loading").SetLeaseUntil(time.Now().Add(-time.Second).Unix()).SetAttempts(1).ExecX(ctx)
	s.runCatalogSnapshot(retry.ID)
	ready := awaitCatalog(t, s, retry.ID, "ready")
	if ready.Attempts != 2 || a.calls.Load() != 2 {
		t.Fatal("crashed reader not recovered")
	}
}
func TestCatalogLateWorkerCannotOverwriteNewLease(t *testing.T) {
	s, c, a := catalogFixture(t)
	a.entered = make(chan struct{}, 1)
	a.release = make(chan struct{})
	ctx := context.Background()
	row, e := s.ensureCatalogSnapshot(ctx, c.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { s.runCatalogSnapshot(row.ID); close(done) }()
	<-a.entered
	s.repo.entClient(ctx).SupplyCatalogSnapshot.UpdateOneID(row.ID).SetStatus("failed").SetMessage("new owner").SetLeaseToken("new-worker").ExecX(ctx)
	close(a.release)
	<-done
	r := s.repo.entClient(ctx).SupplyCatalogSnapshot.GetX(ctx, row.ID)
	if r.Status != "failed" || r.Message != "new owner" {
		t.Fatal("late worker overwrote lease owner")
	}
}
func TestImportedCategoryNamesUseCharactersAndFailBeforeWrites(t *testing.T) {
	s, c, _ := catalogFixture(t)
	ctx := context.Background()
	names := []string{"全球海外邮箱--德国--俄罗斯--拉脱维亚--挪威 等国家本土高权重邮箱", "TK各区千粉万粉自然流/中视频退役号【可自助选号】", "各区满年高权重精品号[作品数量20以上-可自选】", strings.Repeat("中", 100), strings.Repeat("🔑", 100)}
	writer := catalog.NewProductRepoImpl(s.repo.data, nil)
	for _, name := range names {
		cat, e := writer.CreateCategory(ctx, name, 0, "", 0)
		if e != nil {
			t.Fatalf("valid unicode name rejected: %v", e)
		}
		if _, e = writer.UpdateCategory(ctx, cat.ID, name, nil, nil, nil, nil); e != nil {
			t.Fatal(e)
		}
	}
	products := map[string]adapter.Product{"p1": {ID: "p1", CategoryID: "c1"}, "p2": {ID: "p2", CategoryID: "c2"}}
	request := &adminv1.ImportProductsRequest{ConnectionId: c.ID, Codes: []string{"p1", "p2"}, CategoryDrafts: []*adminv1.ImportCategoryDraft{{UpstreamCode: "c1", Name: strings.Repeat("中", 101)}, {UpstreamCode: "c2", Name: "  "}}}
	before := s.repo.entClient(ctx).Category.Query().CountX(ctx)
	_, e := s.saveImportCategories(ctx, request, products, PriceModeEqual, 0, 0)
	if e == nil || !strings.Contains(e.Error(), "c1") || !strings.Contains(e.Error(), "c2") {
		t.Fatal("missing consolidated field errors", e)
	}
	if s.repo.entClient(ctx).Category.Query().CountX(ctx) != before {
		t.Fatal("validation partially wrote categories")
	}
	request.CategoryDrafts[0].Name = names[0]
	request.CategoryDrafts[1].Name = names[1]
	if _, e = s.saveImportCategories(ctx, request, products, PriceModeEqual, 0, 0); e != nil {
		t.Fatal("real-world category draft failed", e)
	}
}
func TestCatalogSnapshotPayloadContainsNoCredentials(t *testing.T) {
	s, c, _ := catalogFixture(t)
	row, e := s.ensureCatalogSnapshot(context.Background(), c.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	s.runCatalogSnapshot(row.ID)
	row = awaitCatalog(t, s, row.ID, "ready")
	var p catalogPayload
	if e = json.Unmarshal(row.Payload, &p); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(row.Payload), "api_secret") || strings.Contains(string(row.Payload), "credentials") {
		t.Fatal("credential leak")
	}
	// All quote/read errors remain read-only; malformed content is rejected.
	row.Payload = []byte("bad")
	if _, e = decodeCatalog(row, c); e == nil {
		t.Fatal(errors.New("invalid payload accepted"))
	}
}

func TestCatalogFailureMessagesAreSafeAndActionable(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		failure    error
	}{
		{"local validation", "商品目录超过最大页数", catalogLoadError("商品目录超过最大页数")},
		{"upstream details", "货源未返回完整有效", errors.New("api_secret=private-upstream-response")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, a := catalogFixture(t)
			a.fail = tc.failure
			row, err := s.ensureCatalogSnapshot(context.Background(), c.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			s.runCatalogSnapshot(row.ID)
			failed := awaitCatalog(t, s, row.ID, "failed")
			if !strings.Contains(failed.Message, tc.want) || strings.Contains(failed.Message, "api_secret") {
				t.Fatal(failed.Message)
			}
		})
	}
}
