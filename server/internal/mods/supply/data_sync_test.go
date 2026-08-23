package supply

// P2-01 T3 必测项：价格保护三级判定（auto_sync_price 关 / 固定覆盖价 /
// 运营已改价不覆盖）+ 状态语义（inactive → 隐藏）。
// P2-10 S1：三类 scope 轻量路径 / 删除对账护栏 / 库存补查 / 增量列表决策。

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

// fakeWriter 记录 UpsertUpstreamProduct 调用（价格保护断言点）。
type fakeWriter struct {
	calls []catalogport.UpstreamProductInput
}

func (f *fakeWriter) UpsertUpstreamProduct(_ context.Context, in catalogport.UpstreamProductInput) (uint64, bool, error) {
	f.calls = append(f.calls, in)
	return 100 + uint64(len(f.calls)), len(f.calls) == 1, nil
}

// fakeMaintainer 记录轻量维护调用（scope=price/status 与删除对账断言点）。
type fakeMaintainer struct {
	priceCalls  map[string]int64
	statusCalls map[string]int8
	shelveSeen  []string
	shelveCount int64
}

func newFakeMaintainer() *fakeMaintainer {
	return &fakeMaintainer{priceCalls: map[string]int64{}, statusCalls: map[string]int8{}}
}

func (f *fakeMaintainer) UpdateUpstreamPrice(_ context.Context, _ uint64, code string, price int64) (bool, error) {
	f.priceCalls[code] = price
	return true, nil
}

func (f *fakeMaintainer) UpdateUpstreamStatus(_ context.Context, _ uint64, code string, status int8) (bool, error) {
	f.statusCalls[code] = status
	return true, nil
}

func (f *fakeMaintainer) ShelveOffMissing(_ context.Context, _ uint64, seen []string) (int64, error) {
	f.shelveSeen = append(f.shelveSeen, seen...)
	return f.shelveCount, nil
}

func newTestSyncService(t *testing.T) (*SyncService, *SupplyRepoImpl, *fakeWriter, *fakeMaintainer) {
	t.Helper()
	repo, _ := newTestRepo(t)
	fw := &fakeWriter{}
	fm := newFakeMaintainer()
	svc := &SyncService{repo: repo, writer: fw, maintainer: fm, log: slog.Default()}
	return svc, repo, fw, fm
}

// collectTask 采集任务（历史兼容：空 scope = collect）。
func collectTask() *ent.SupplySyncTask {
	return &ent.SupplySyncTask{Scope: ""}
}

// seedConnAndMapping 建连接 + 映射（可选 pricing_override）。
func seedConnAndMapping(t *testing.T, repo *SupplyRepoImpl, autoSync bool, override map[string]any) *ent.SupplyConnection {
	t.Helper()
	conn := mustConn(t, repo, nil, "保护测试")
	conn.AutoSyncPrice = autoSync
	ctx := context.Background()
	if err := repo.UpsertMapping(ctx, &ent.SupplyMapping{
		ConnectionID:    conn.ID,
		UpstreamProduct: "P1",
		LocalProductID:  50,
		PricingOverride: override,
	}); err != nil {
		t.Fatal(err)
	}
	return conn
}

// connIDOf 建一个连接并返回 ID（子测试内联建连接用）。
func connIDOf(t *testing.T, repo *SupplyRepoImpl) uint64 {
	t.Helper()
	return mustConn(t, repo, nil, "占位连接").ID
}

func TestPriceProtection(t *testing.T) {
	ctx := context.Background()
	upstream := &adapter.Product{ID: "P1", Name: "上游商品", Price: 1000, IsActive: true}

	t.Run("auto_sync_price关闭_不改价", func(t *testing.T) {
		svc, repo, fw, _ := newTestSyncService(t)
		conn := seedConnAndMapping(t, repo, false, nil)
		stats := &TaskProgress{}
		if _, err := svc.syncOne(ctx, 0, collectTask(), conn, upstream, nil, stats); err != nil {
			t.Fatal(err)
		}
		if len(fw.calls) != 1 || fw.calls[0].Price != -1 {
			t.Fatalf("auto_sync_price=false 必须传 Price=-1: %+v", fw.calls)
		}
		if stats.ManualSkipped != 1 {
			t.Fatalf("manual_skipped 应 +1: %+v", stats)
		}
	})

	t.Run("固定覆盖价", func(t *testing.T) {
		svc, repo, fw, _ := newTestSyncService(t)
		conn := seedConnAndMapping(t, repo, true, map[string]any{"price": int64(8888)})
		stats := &TaskProgress{}
		if _, err := svc.syncOne(ctx, 0, collectTask(), conn, upstream, nil, stats); err != nil {
			t.Fatal(err)
		}
		if fw.calls[0].Price != 8888 {
			t.Fatalf("固定覆盖价应写入 8888: %+v", fw.calls[0])
		}
		if stats.PriceUpdated != 1 {
			t.Fatalf("price_updated 应 +1: %+v", stats)
		}
	})

	t.Run("运营改价_保护不覆盖", func(t *testing.T) {
		svc, repo, fw, _ := newTestSyncService(t)
		ctx := context.Background()
		// 先建本地商品（价格 9999 = 运营改过），拿到真实 ID
		client := repo.entClient(ctx)
		local, err := client.Product.Create().
			SetSubsiteID(0).
			SetName("本地商品").
			SetSlug("local-50").
			SetPrice(9999).
			SetUpstreamSourceID(connIDOf(t, repo)).
			SetUpstreamProductCode("P1").
			Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conn := mustConn(t, repo, nil, "保护测试")
		conn.AutoSyncPrice = true
		// 上次同步价 1000（映射记录）；本地商品已被运营改为 9999
		if err := repo.UpsertMapping(ctx, &ent.SupplyMapping{
			ConnectionID:    conn.ID,
			UpstreamProduct: "P1",
			LocalProductID:  local.ID,
			PricingOverride: map[string]any{"last_synced_price": int64(1000)},
		}); err != nil {
			t.Fatal(err)
		}
		stats := &TaskProgress{}
		if _, err := svc.syncOne(ctx, 0, collectTask(), conn, upstream, nil, stats); err != nil {
			t.Fatal(err)
		}
		if fw.calls[0].Price != -1 {
			t.Fatalf("运营改价必须保护（Price=-1）: %+v", fw.calls[0])
		}
		if stats.ManualSkipped != 1 {
			t.Fatalf("manual_skipped 应 +1: %+v", stats)
		}
		// 基线更新为运营价（后续同步保持）
		m, err := repo.GetMapping(ctx, conn.ID, "P1", "")
		if err != nil {
			t.Fatal(err)
		}
		if toInt64(m.PricingOverride["last_synced_price"]) != 9999 {
			t.Fatalf("基线应更新为运营价 9999: %+v", m.PricingOverride)
		}
	})

	t.Run("正常同步_更新价格并记基线", func(t *testing.T) {
		svc, repo, fw, _ := newTestSyncService(t)
		conn := seedConnAndMapping(t, repo, true, nil)
		stats := &TaskProgress{}
		if _, err := svc.syncOne(ctx, 0, collectTask(), conn, upstream, nil, stats); err != nil {
			t.Fatal(err)
		}
		if fw.calls[0].Price != 1000 {
			t.Fatalf("应写入新价 1000: %+v", fw.calls[0])
		}
		if stats.PriceUpdated != 1 {
			t.Fatalf("price_updated 应 +1: %+v", stats)
		}
		m, err := repo.GetMapping(ctx, conn.ID, "P1", "")
		if err != nil {
			t.Fatal(err)
		}
		if toInt64(m.PricingOverride["last_synced_price"]) != 1000 {
			t.Fatalf("基线应记录 1000: %+v", m.PricingOverride)
		}
		// 映射 up_stock 缓存
		if m.UpStock != 0 { // 测试商品无 stock 字段 → 0
			t.Fatalf("up_stock 缓存错误: %+v", m)
		}
	})
}

func TestSyncStatusSemantics(t *testing.T) {
	ctx := context.Background()
	svc, repo, fw, _ := newTestSyncService(t)
	conn := seedConnAndMapping(t, repo, true, nil)

	t.Run("上游inactive_本地隐藏", func(t *testing.T) {
		up := &adapter.Product{ID: "P2", Name: "下架商品", Price: 500, IsActive: false}
		if _, err := svc.syncOne(ctx, 0, collectTask(), conn, up, nil, &TaskProgress{}); err != nil {
			t.Fatal(err)
		}
		if fw.calls[0].Status != 2 {
			t.Fatalf("inactive 应隐藏(status=2): %+v", fw.calls[0])
		}
	})
}

// ---- P2-10 S1：scope 轻量路径 / 对账护栏 / 库存补查 / 增量决策 ----

// fakeUpstream 最小适配器（runLoop/backfill 测试用）。
type fakeUpstream struct {
	products []adapter.Product
	total    int // 护栏测试可虚报
	echo     bool
	stocks   map[string]int32
}

func (f *fakeUpstream) Protocol() string                          { return "fake" }
func (f *fakeUpstream) Ping(context.Context) (*adapter.PingResult, error) { return nil, nil }
func (f *fakeUpstream) ListCategories(context.Context) ([]adapter.Category, error) {
	return nil, nil
}
func (f *fakeUpstream) ListProducts(_ context.Context, _, _ int, _ bool) (*adapter.ProductList, error) {
	return &adapter.ProductList{Total: f.total, Items: f.products, IncludesInactive: f.echo}, nil
}
func (f *fakeUpstream) GetStock(_ context.Context, code, _ string) (int32, error) {
	if v, ok := f.stocks[code]; ok {
		return v, nil
	}
	return 0, context.DeadlineExceeded // 模拟查询失败（fail-open 判据）
}
func (f *fakeUpstream) CreateOrder(context.Context, adapter.CreateOrderReq) (*adapter.CreateOrderResult, error) {
	return nil, nil
}
func (f *fakeUpstream) GetOrder(context.Context, string) (*adapter.OrderDetail, error) {
	return nil, nil
}
func (f *fakeUpstream) RefundOrder(context.Context, string) error { return nil }

func listOf(a adapter.Adapter) func(ctx context.Context, page, pageSize int) (*adapter.ProductList, error) {
	return func(ctx context.Context, page, pageSize int) (*adapter.ProductList, error) {
		return a.ListProducts(ctx, page, pageSize, true)
	}
}

func TestPriceScopeLightPath(t *testing.T) {
	ctx := context.Background()
	svc, repo, fw, fm := newTestSyncService(t)
	conn := seedConnAndMapping(t, repo, true, nil) // P1 已映射
	task := &ent.SupplySyncTask{Scope: ScopePrice}

	t.Run("已映射_仅刷价", func(t *testing.T) {
		up := &adapter.Product{ID: "P1", Name: "上游商品", Price: 2000, IsActive: true}
		if _, err := svc.syncOne(ctx, 0, task, conn, up, nil, &TaskProgress{}); err != nil {
			t.Fatal(err)
		}
		if got := fm.priceCalls["P1"]; got != 2000 {
			t.Fatalf("price scope 应经 maintainer 刷价 2000: %v", fm.priceCalls)
		}
		if len(fw.calls) != 0 {
			t.Fatalf("price scope 不得走全量 upsert: %+v", fw.calls)
		}
	})

	t.Run("未映射_跳过不创建", func(t *testing.T) {
		before := len(fm.priceCalls)
		up := &adapter.Product{ID: "NEW", Name: "新商品", Price: 100, IsActive: true}
		if _, err := svc.syncOne(ctx, 0, task, conn, up, nil, &TaskProgress{}); err != nil {
			t.Fatal(err)
		}
		if len(fm.priceCalls) != before || len(fw.calls) != 0 {
			t.Fatalf("未映射商品应跳过: %v", fm.priceCalls)
		}
	})

	t.Run("auto_sync_price关闭_不刷", func(t *testing.T) {
		svc2, repo2, _, fm2 := newTestSyncService(t)
		conn2 := seedConnAndMapping(t, repo2, false, nil)
		up := &adapter.Product{ID: "P1", Name: "上游商品", Price: 3000, IsActive: true}
		stats := &TaskProgress{}
		if _, err := svc2.syncOne(ctx, 0, task, conn2, up, nil, stats); err != nil {
			t.Fatal(err)
		}
		if len(fm2.priceCalls) != 0 {
			t.Fatalf("auto_sync_price=false 不应刷价: %v", fm2.priceCalls)
		}
		if stats.ManualSkipped != 1 {
			t.Fatalf("应计 manual_skipped: %+v", stats)
		}
	})
}

func TestStatusScopeLightPath(t *testing.T) {
	ctx := context.Background()
	svc, repo, fw, fm := newTestSyncService(t)
	conn := seedConnAndMapping(t, repo, true, nil)
	task := &ent.SupplySyncTask{Scope: ScopeStatus}

	t.Run("上游inactive_本地隐藏", func(t *testing.T) {
		up := &adapter.Product{ID: "P1", IsActive: false}
		if _, err := svc.syncOne(ctx, 0, task, conn, up, nil, &TaskProgress{}); err != nil {
			t.Fatal(err)
		}
		if fm.statusCalls["P1"] != 2 {
			t.Fatalf("inactive 应写 status=2: %v", fm.statusCalls)
		}
		if len(fw.calls) != 0 {
			t.Fatalf("status scope 不得走全量 upsert: %+v", fw.calls)
		}
	})

	t.Run("库存已知才更新缓存", func(t *testing.T) {
		up := &adapter.Product{ID: "P1", IsActive: true, Stock: 7}
		if _, err := svc.syncOne(ctx, 0, task, conn, up, nil, &TaskProgress{}); err != nil {
			t.Fatal(err)
		}
		m, _ := repo.GetMapping(ctx, conn.ID, "P1", "")
		if m.UpStock != 7 {
			t.Fatalf("已知库存应更新缓存: %+v", m)
		}
		up2 := &adapter.Product{ID: "P1", IsActive: true, Stock: -1}
		if _, err := svc.syncOne(ctx, 0, task, conn, up2, nil, &TaskProgress{}); err != nil {
			t.Fatal(err)
		}
		m2, _ := repo.GetMapping(ctx, conn.ID, "P1", "")
		if m2.UpStock != 7 {
			t.Fatalf("未知库存(-1)不得覆盖缓存: %+v", m2)
		}
	})
}

func TestReconcileGuardAndShelve(t *testing.T) {
	ctx := context.Background()

	t.Run("护栏_上游虚报总数_任务失败不对账", func(t *testing.T) {
		svc, repo, _, fm := newTestSyncService(t)
		conn := mustConn(t, repo, nil, "护栏")
		task, _ := repo.CreateSyncTask(ctx, conn.ID, "full", ScopeCollect, false)
		up := &fakeUpstream{
			products: []adapter.Product{{ID: "A", Name: "A", IsActive: true}, {ID: "B", Name: "B", IsActive: true}},
			total:    5, // 声称 5 件只给 2 件（分页不完整）
			echo:     true,
		}
		if err := svc.runLoop(ctx, task.ID, task, conn, up, loadScheduleSettings(conn), ScopeCollect, false, listOf(up)); err != nil {
			t.Fatal(err)
		}
		got, _ := repo.GetSyncTask(ctx, task.ID)
		if got.Status != "failed" || got.ErrorCode != "RECONCILE_GUARD" {
			t.Fatalf("护栏应判 failed/RECONCILE_GUARD: %+v", got)
		}
		if len(fm.shelveSeen) != 0 {
			t.Fatalf("护栏触发时不得对账下架: %v", fm.shelveSeen)
		}
	})

	t.Run("权威快照_对账并写锚点", func(t *testing.T) {
		svc, repo, _, fm := newTestSyncService(t)
		conn := mustConn(t, repo, nil, "对账")
		task, _ := repo.CreateSyncTask(ctx, conn.ID, "full", ScopeCollect, false)
		up := &fakeUpstream{
			products: []adapter.Product{{ID: "A", Name: "A", IsActive: true}, {ID: "B", Name: "B", IsActive: true}},
			total:    2,
			echo:     true,
		}
		if err := svc.runLoop(ctx, task.ID, task, conn, up, loadScheduleSettings(conn), ScopeCollect, false, listOf(up)); err != nil {
			t.Fatal(err)
		}
		got, _ := repo.GetSyncTask(ctx, task.ID)
		if got.Status != "done" {
			t.Fatalf("权威快照应 done: %+v", got)
		}
		if len(fm.shelveSeen) != 2 { // seen 交给对账方（本地由 maintainer 决定谁下架）
			t.Fatalf("应携带 seen 集对账: %v", fm.shelveSeen)
		}
		// 锚点写入（增量依据）
		fresh, _ := repo.GetConnection(ctx, conn.ID)
		if readSyncAnchor(fresh, ScopeCollect).IsZero() {
			t.Fatal("collect 锚点应写入 settings.sync_anchors")
		}
	})

	t.Run("无回声_禁用对账", func(t *testing.T) {
		svc, repo, _, fm := newTestSyncService(t)
		conn := mustConn(t, repo, nil, "无回声")
		task, _ := repo.CreateSyncTask(ctx, conn.ID, "full", ScopeCollect, false)
		up := &fakeUpstream{
			products: []adapter.Product{{ID: "A", Name: "A", IsActive: true}},
			total:    1,
			echo:     false, // 旧版上游不识别 include_inactive
		}
		if err := svc.runLoop(ctx, task.ID, task, conn, up, loadScheduleSettings(conn), ScopeCollect, false, listOf(up)); err != nil {
			t.Fatal(err)
		}
		got, _ := repo.GetSyncTask(ctx, task.ID)
		if got.Status != "done" {
			t.Fatalf("无回声仍应 done（仅禁用对账）: %+v", got)
		}
		if len(fm.shelveSeen) != 0 {
			t.Fatalf("无回声不得对账: %v", fm.shelveSeen)
		}
	})
}

func TestBackfillStocks(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestSyncService(t)
	up := &fakeUpstream{stocks: map[string]int32{"A": 5, "B": 0}}
	items := []adapter.Product{
		{ID: "A", Stock: -1},
		{ID: "B", Stock: -1},
		{ID: "C", Stock: -1}, // 查询失败 → 保持 -1（fail-open）
		{ID: "D", Stock: 9},  // 已知库存不补查
	}
	if err := svc.backfillStocks(ctx, up, loadScheduleSettings(nil), items, 0); err != nil {
		t.Fatal(err)
	}
	if items[0].Stock != 5 || items[1].Stock != 0 {
		t.Fatalf("补查应回填真实库存: %+v", items)
	}
	if items[2].Stock != -1 {
		t.Fatalf("失败项应保持 -1: %+v", items)
	}
	if items[3].Stock != 9 {
		t.Fatalf("已知库存不得覆盖: %+v", items)
	}
}

func TestResolveListerIncremental(t *testing.T) {
	log := slog.Default()
	// 无锚点 → 全量
	l, inc := resolveLister(&fakeUpstream{}, &ent.SupplySyncTask{Mode: "incremental"}, time.Time{}, log)
	if inc {
		t.Fatal("无锚点应全量")
	}
	// 有锚点但驱动不支持增量 → 全量回落
	l, inc = resolveLister(&fakeUpstream{}, &ent.SupplySyncTask{Mode: "incremental"}, time.Now(), log)
	if inc {
		t.Fatal("fake 驱动不支持增量应回落全量")
	}
	_ = l
	// dujiao 支持增量（覆盖在 adapter_test 的 TestDujiaoIncrementalList）
}
