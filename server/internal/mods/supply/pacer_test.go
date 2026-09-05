package supply

// S2 节奏器（AIMD）测试：降速倍增/熔断指数递增/半开探测/回升/重置。

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
)

// purchaseReq 测试用采购请求。
func purchaseReq(connID uint64) supplyport.PurchaseRequest {
	return supplyport.PurchaseRequest{ConnectionID: connID, ProductCode: "P1", Quantity: 1}
}

func newTestPacer(t *testing.T) (*Pacer, *SupplyRepoImpl) {
	t.Helper()
	repo, _ := newTestRepo(t)
	return NewPacer(repo, nil, slog.Default()), repo
}

func TestPacerAIMD(t *testing.T) {
	ctx := context.Background()
	p, repo := newTestPacer(t)
	conn := mustConn(t, repo, nil, "节奏器")

	t.Run("初始为配置底线", func(t *testing.T) {
		if d := p.Delay(conn); d != loadScheduleSettings(conn).PageDelay {
			t.Fatalf("初始应等于配置底线: %v", d)
		}
	})

	t.Run("单次限流_间隔倍增不熔断", func(t *testing.T) {
		if p.OnRateLimited(ctx, conn, "429") {
			t.Fatal("单次限流不应熔断")
		}
		if d := p.Delay(conn); d != 2*loadScheduleSettings(conn).PageDelay {
			t.Fatalf("间隔应倍增: %v", d)
		}
		fresh, _ := repo.GetConnection(ctx, conn.ID)
		if !fresh.RateLimitUntil.IsZero() {
			t.Fatal("单次限流不应写熔断截止")
		}
	})

	t.Run("连续两次_熔断五分钟", func(t *testing.T) {
		if !p.OnRateLimited(ctx, conn, "429 again") {
			t.Fatal("连续两次应熔断")
		}
		fresh, _ := repo.GetConnection(ctx, conn.ID)
		remain := time.Until(fresh.RateLimitUntil)
		if remain < 4*time.Minute || remain > 5*time.Minute {
			t.Fatalf("首次冷却应约 5 分钟: %v", remain)
		}
		if !p.CooldownActive(fresh) {
			t.Fatal("冷却期内应判定 active")
		}
		snap := p.Snapshot(fresh)
		if snap.BlockedCount != 1 || snap.CooldownSec != int64(pacerCooldownBase.Seconds()) {
			t.Fatalf("熔断计数/冷却错误: %+v", snap)
		}
	})

	t.Run("冷却到期_半开探测成功解除", func(t *testing.T) {
		// 手动把截止改到过去（模拟到期）
		past := time.Now().UTC().Add(-time.Minute)
		if err := repo.TripRateLimit(ctx, conn.ID, past, map[string]any{}, ""); err != nil {
			t.Fatal(err)
		}
		fresh, _ := repo.GetConnection(ctx, conn.ID)
		if p.CooldownActive(fresh) {
			t.Fatal("到期后应放行（半开）")
		}
		p.OnSuccess(ctx, fresh) // 探测成功 → 解除
		fresh2, _ := repo.GetConnection(ctx, conn.ID)
		if !fresh2.RateLimitUntil.IsZero() {
			t.Fatal("探测成功应清熔断截止")
		}
		if snap := p.Snapshot(fresh2); snap.CooldownSec != 0 {
			t.Fatalf("解除后冷却时长应归零: %+v", snap)
		}
	})

	t.Run("半开探测失败_冷却翻倍", func(t *testing.T) {
		// 重新熔断（探测成功解除后归零 → 本次又是基础 5 分钟）
		fresh, _ := repo.GetConnection(ctx, conn.ID)
		p.OnRateLimited(ctx, fresh, "429")
		fresh2, _ := repo.GetConnection(ctx, conn.ID)
		if !p.OnRateLimited(ctx, fresh2, "429") {
			t.Fatal("应再次熔断")
		}
		// 模拟到期 → 半开 → 探测再失败：冷却翻倍（10 分钟）
		past := time.Now().UTC().Add(-time.Minute)
		if err := repo.TripRateLimit(ctx, conn.ID, past, map[string]any{}, ""); err != nil {
			t.Fatal(err)
		}
		fresh3, _ := repo.GetConnection(ctx, conn.ID)
		if p.CooldownActive(fresh3) {
			t.Fatal("到期应进入半开")
		}
		if !p.OnRateLimited(ctx, fresh3, "429") {
			t.Fatal("半开失败应立即再熔断")
		}
		fresh4, _ := repo.GetConnection(ctx, conn.ID)
		if snap := p.Snapshot(fresh4); snap.CooldownSec != int64((2 * pacerCooldownBase).Seconds()) {
			t.Fatalf("半开失败冷却应翻倍至 10 分钟: %+v", snap)
		}
	})

	t.Run("连续成功_间隔回落", func(t *testing.T) {
		fresh, _ := repo.GetConnection(ctx, conn.ID)
		before := p.Delay(fresh)
		if before <= loadScheduleSettings(fresh).PageDelay {
			t.Fatal("前置条件：当前间隔应高于底线")
		}
		for i := 0; i < pacerRecoverStreak; i++ {
			p.OnSuccess(ctx, fresh)
		}
		after := p.Delay(fresh)
		if after >= before {
			t.Fatalf("连续成功后间隔应回落: before=%v after=%v", before, after)
		}
	})

	t.Run("重置", func(t *testing.T) {
		fresh, _ := repo.GetConnection(ctx, conn.ID)
		p.OnRateLimited(ctx, fresh, "429")
		if err := p.Reset(ctx, conn.ID); err != nil {
			t.Fatal(err)
		}
		fresh2, _ := repo.GetConnection(ctx, conn.ID)
		if !fresh2.RateLimitUntil.IsZero() || len(fresh2.RateState) != 0 {
			t.Fatalf("重置应清状态: %+v", fresh2)
		}
		if d := p.Delay(fresh2); d != loadScheduleSettings(fresh2).PageDelay {
			t.Fatalf("重置后应回底线: %v", d)
		}
	})
}

// TestGatewayCooldownGate 采购网关熔断拦截（ S2：出站共享节奏器）。
func TestGatewayCooldownGate(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepo(t)
	p := NewPacer(repo, nil, slog.Default())
	gw := NewGateway(repo, p, nil)
	conn := mustConn(t, repo, nil, "网关熔断")

	// 未熔断：正常放行到适配器层（base_url 置为非法值令装配快速失败——
	// 断言点只是「不是 ErrCooldownActive」）
	_, _ = repo.entClient(ctx).SupplyConnection.UpdateOneID(conn.ID).SetBaseURL("://bad").Save(ctx)
	_, err := gw.Submit(ctx, purchaseReq(conn.ID))
	if errors.Is(err, ErrCooldownActive) {
		t.Fatalf("未熔断不应拦截: %v", err)
	}
	// 还原（熔断路径在 gate 层不触适配器，但保持一致性）
	_, _ = repo.entClient(ctx).SupplyConnection.UpdateOneID(conn.ID).SetBaseURL("https://up.example.com").Save(ctx)

	// 熔断 → 拦截
	p.OnRateLimited(ctx, conn, "429")
	p.OnRateLimited(ctx, conn, "429")
	if _, err := gw.Submit(ctx, purchaseReq(conn.ID)); !errors.Is(err, ErrCooldownActive) {
		t.Fatalf("熔断期应拦截: %v", err)
	}
	if _, err := gw.Query(ctx, conn.ID, "T-1"); !errors.Is(err, ErrCooldownActive) {
		t.Fatalf("查询同样拦截: %v", err)
	}
}

// TestRunSyncCooldownGate 同步任务在熔断期直接失败留痕。
func TestRunSyncCooldownGate(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepo(t)
	p := NewPacer(repo, nil, slog.Default())
	svc := &SyncService{repo: repo, pacer: p, log: slog.Default()}
	conn := mustConn(t, repo, nil, "同步熔断")
	task, err := repo.CreateSyncTask(ctx, conn.ID, "full", ScopeCollect, false)
	if err != nil {
		t.Fatal(err)
	}
	p.OnRateLimited(ctx, conn, "429")
	p.OnRateLimited(ctx, conn, "429")

	if err := svc.RunSync(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetSyncTask(ctx, task.ID)
	if got.Status != "failed" || got.ErrorCode != "RATE_LIMITED_COOLDOWN" {
		t.Fatalf("熔断期任务应失败留痕: %+v", got)
	}
}
