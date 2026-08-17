package supply

// P2-01 T3 必测项：价格保护三级判定（auto_sync_price 关 / 固定覆盖价 /
// 运营已改价不覆盖）+ 状态语义（inactive → 隐藏）。

import (
	"context"
	"log/slog"
	"testing"

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

func newTestSyncService(t *testing.T) (*SyncService, *SupplyRepoImpl, *fakeWriter) {
	t.Helper()
	repo, _ := newTestRepo(t)
	fw := &fakeWriter{}
	svc := &SyncService{repo: repo, writer: fw, log: slog.Default()}
	return svc, repo, fw
}

// seedConnAndMapping 建连接 + 映射（可选 pricing_override）。
func seedConnAndMapping(t *testing.T, repo *SupplyRepoImpl, autoSync bool, override map[string]any) *ent.SupplyConnection {
	t.Helper()
	conn := mustConn(t, repo, nil, "保护测试")
	conn.AutoSyncPrice = autoSync
	ctx := context.Background()
	if _, err := repo.UpsertMapping(ctx, &ent.SupplyMapping{
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
		svc, repo, fw := newTestSyncService(t)
		conn := seedConnAndMapping(t, repo, false, nil)
		stats := &TaskProgress{}
		if _, err := svc.syncOne(ctx, 0, conn, upstream, nil, stats); err != nil {
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
		svc, repo, fw := newTestSyncService(t)
		conn := seedConnAndMapping(t, repo, true, map[string]any{"price": int64(8888)})
		stats := &TaskProgress{}
		if _, err := svc.syncOne(ctx, 0, conn, upstream, nil, stats); err != nil {
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
		svc, repo, fw := newTestSyncService(t)
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
		if _, err := repo.UpsertMapping(ctx, &ent.SupplyMapping{
			ConnectionID:    conn.ID,
			UpstreamProduct: "P1",
			LocalProductID:  local.ID,
			PricingOverride: map[string]any{"last_synced_price": int64(1000)},
		}); err != nil {
			t.Fatal(err)
		}
		stats := &TaskProgress{}
		if _, err := svc.syncOne(ctx, 0, conn, upstream, nil, stats); err != nil {
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
		svc, repo, fw := newTestSyncService(t)
		conn := seedConnAndMapping(t, repo, true, nil)
		stats := &TaskProgress{}
		if _, err := svc.syncOne(ctx, 0, conn, upstream, nil, stats); err != nil {
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
	svc, repo, fw := newTestSyncService(t)
	conn := seedConnAndMapping(t, repo, true, nil)

	t.Run("上游inactive_本地隐藏", func(t *testing.T) {
		up := &adapter.Product{ID: "P2", Name: "下架商品", Price: 500, IsActive: false}
		if _, err := svc.syncOne(ctx, 0, conn, up, nil, &TaskProgress{}); err != nil {
			t.Fatal(err)
		}
		if fw.calls[0].Status != 2 {
			t.Fatalf("inactive 应隐藏(status=2): %+v", fw.calls[0])
		}
	})
}
