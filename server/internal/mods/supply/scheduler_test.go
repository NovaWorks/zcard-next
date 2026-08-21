package supply

// P2-10 S3 调度器测试：时间窗口（含跨午夜）/到期判定/防重入/熔断跳过/看门狗。

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
)

func TestInWindows(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 8, 20, h, m, 0, 0, time.Local) }
	cases := []struct {
		name    string
		windows []timeWindow
		at      time.Time
		want    bool
	}{
		{"空窗口=全天", nil, at(3, 0), true},
		{"窗口内", []timeWindow{{"09:00", "18:00"}}, at(12, 0), true},
		{"窗口外", []timeWindow{{"09:00", "18:00"}}, at(20, 0), false},
		{"边界start含", []timeWindow{{"09:00", "18:00"}}, at(9, 0), true},
		{"边界end不含", []timeWindow{{"09:00", "18:00"}}, at(18, 0), false},
		{"跨午夜前段", []timeWindow{{"22:00", "06:00"}}, at(23, 30), true},
		{"跨午夜后段", []timeWindow{{"22:00", "06:00"}}, at(2, 0), true},
		{"跨午夜白天", []timeWindow{{"22:00", "06:00"}}, at(12, 0), false},
		{"多窗口任一", []timeWindow{{"01:00", "02:00"}, {"22:00", "23:00"}}, at(22, 30), true},
		{"非法窗口忽略", []timeWindow{{"25:00", "26:00"}}, at(12, 0), false},
	}
	for _, c := range cases {
		if got := inWindows(c.windows, c.at); got != c.want {
			t.Fatalf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestParseScheduleConfig(t *testing.T) {
	conn := &ent.SupplyConnection{Settings: map[string]any{
		"schedule": map[string]any{
			"enabled": true,
			"collect": map[string]any{"enabled": true, "mode": "full", "interval": float64(120), "windows": []any{map[string]any{"start": "01:00", "end": "05:00"}}},
			"price":   map[string]any{"enabled": true}, // interval 缺省 → 默认 30
		},
	}}
	cfg := loadScheduleConfig(conn)
	if !cfg.Enabled {
		t.Fatal("schedule.enabled 应为 true")
	}
	col := cfg.Scopes[ScopeCollect]
	if !col.Enabled || col.Mode != "full" || col.Interval != 120 || len(col.Windows) != 1 {
		t.Fatalf("collect 解析错误: %+v", col)
	}
	if got := cfg.Scopes[ScopePrice].Interval; got != 30 {
		t.Fatalf("price 缺省间隔应为 30: %d", got)
	}
	if st, ok := cfg.Scopes[ScopeStatus]; ok && st.Enabled {
		t.Fatal("未配置的 scope 不应启用")
	}
	// 无 schedule 键 = 关闭
	if loadScheduleConfig(&ent.SupplyConnection{}).Enabled {
		t.Fatal("无 schedule 应关闭")
	}
}

// fmtHHMM 分钟数 → "HH:MM"。
func fmtHHMM(minutes int) string {
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

func newTestScheduler(t *testing.T) (*Scheduler, *SupplyRepoImpl, *SyncService) {
	t.Helper()
	repo, _ := newTestRepo(t)
	svc := &SyncService{repo: repo, log: slog.Default()} // enq 降级路径：StartTask 进程内异步
	return NewScheduler(repo, svc, slog.Default()), repo, svc
}

// schedConn 建带 schedule 配置的连接。
func schedConn(t *testing.T, repo *SupplyRepoImpl, sched map[string]any) *ent.SupplyConnection {
	t.Helper()
	conn := mustConn(t, repo, nil, "调度")
	_, err := repo.entClient(context.Background()).SupplyConnection.UpdateOneID(conn.ID).
		SetSettings(map[string]any{"schedule": sched}).
		SetStatus(supplyconnection.StatusActive).
		Save(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := repo.GetConnection(context.Background(), conn.ID)
	if err != nil {
		t.Fatal(err)
	}
	return fresh
}

func TestSchedulerScan(t *testing.T) {
	ctx := context.Background()
	always := map[string]any{
		"enabled": true,
		"collect": map[string]any{"enabled": true, "interval": 60, "windows": []any{}},
	}

	t.Run("到期派发_锚点回写", func(t *testing.T) {
		s, repo, _ := newTestScheduler(t)
		conn := schedConn(t, repo, always)
		s.Scan(ctx)
		tasks, _, _ := repo.ListSyncTasks(ctx, conn.ID, 1, 10)
		if len(tasks) != 1 || tasks[0].Scope != ScopeCollect {
			t.Fatalf("应派发一个 collect 任务: %+v", tasks)
		}
		fresh, _ := repo.GetConnection(ctx, conn.ID)
		if fresh.LastCollectAt.IsZero() {
			t.Fatal("锚点应回写")
		}
		// 二轮扫描：锚点刚写过 → 不再派发（防重入）
		s.Scan(ctx)
		tasks2, _, _ := repo.ListSyncTasks(ctx, conn.ID, 1, 10)
		if len(tasks2) != 1 {
			t.Fatalf("间隔内不应重复派发: %+v", tasks2)
		}
	})

	t.Run("窗口外跳过", func(t *testing.T) {
		s, repo, _ := newTestScheduler(t)
		// 用「全天中点两侧」构造确定性窗口：取当前分钟 ±1 的窗口使now在窗外
		now := time.Now()
		end := (now.Hour()*60 + now.Minute() - 2 + 1440) % 1440 // 2 分钟前
		conn := schedConn(t, repo, map[string]any{
			"enabled": true,
			"collect": map[string]any{"enabled": true, "interval": 1, "windows": []any{map[string]any{
				"start": fmtHHMM((end - 30 + 1440) % 1440), "end": fmtHHMM(end),
			}}},
		})
		s.Scan(ctx)
		tasks, _, _ := repo.ListSyncTasks(ctx, conn.ID, 1, 10)
		if len(tasks) != 0 {
			t.Fatalf("窗口外不应派发: %+v", tasks)
		}
	})

	t.Run("熔断冷却跳过", func(t *testing.T) {
		s, repo, _ := newTestScheduler(t)
		conn := schedConn(t, repo, always)
		if err := repo.TripRateLimit(ctx, conn.ID, time.Now().UTC().Add(5*time.Minute), map[string]any{}, "上游限流"); err != nil {
			t.Fatal(err)
		}
		s.Scan(ctx)
		tasks, _, _ := repo.ListSyncTasks(ctx, conn.ID, 1, 10)
		if len(tasks) != 0 {
			t.Fatalf("冷却期不应派发: %+v", tasks)
		}
	})

	t.Run("总开关关闭跳过", func(t *testing.T) {
		s, repo, _ := newTestScheduler(t)
		conn := schedConn(t, repo, map[string]any{"enabled": false, "collect": map[string]any{"enabled": true, "interval": 60}})
		s.Scan(ctx)
		tasks, _, _ := repo.ListSyncTasks(ctx, conn.ID, 1, 10)
		if len(tasks) != 0 {
			t.Fatalf("总开关关闭不应派发: %+v", tasks)
		}
	})
}

func TestReapStaleTasks(t *testing.T) {
	ctx := context.Background()
	s, repo, _ := newTestScheduler(t)
	conn := schedConn(t, repo, map[string]any{"enabled": false})

	// 僵死任务：processing 且心跳超 10 分钟
	stale, err := repo.CreateSyncTask(ctx, conn.ID, "full", ScopeCollect, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetTaskProcessing(ctx, stale.ID, 0); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-15 * time.Minute)
	_, _ = repo.entClient(ctx).SupplySyncTask.UpdateOneID(stale.ID).SetHeartbeatAt(old).Save(ctx)

	s.ReapStaleTasks(ctx)
	got, _ := repo.GetSyncTask(ctx, stale.ID)
	if got.Status != "failed" || got.ErrorCode != "STALE_HEARTBEAT" {
		t.Fatalf("僵死任务应被回收: %+v", got)
	}

	// 新鲜任务不受影响
	fresh, _ := repo.CreateSyncTask(ctx, conn.ID, "full", ScopeCollect, false)
	_ = repo.SetTaskProcessing(ctx, fresh.ID, 0)
	s.ReapStaleTasks(ctx)
	got2, _ := repo.GetSyncTask(ctx, fresh.ID)
	if got2.Status != "processing" {
		t.Fatalf("新鲜任务不应被回收: %+v", got2)
	}
}
