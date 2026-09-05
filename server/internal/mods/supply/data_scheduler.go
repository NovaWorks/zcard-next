package supply

// 定时调度器（ S3）：cron 每分钟扫描 active 连接，按 settings.schedule 派发
// collect/price/status 三类同步任务。
//
// - 到期判定：enabled && interval>0 && 当前时间在窗口内 && 距锚点 ≥ 间隔
// - 熔断冷却中的渠道跳过（rate_limit_until 列；节奏器半开探测由出站路径负责）
// - 防重入：同连接存在 pending/processing 任务时本轮跳过（任务状态机兜底）
// - 锚点在派发时即回写（宁可晚一轮也绝不密集双发；任务失败下轮重试）
// - 看门狗：心跳超 10 分钟的 processing 任务置 failed（防僵死饿死调度）
//
// settings.schedule 结构（界面可编辑，对齐 1.x SupplyScheduleService）：
//
//	{
// "enabled": true, "request_delay": 1, "stock_concurrency": 3,
// "stock_request_delay_ms": 200,
// "collect": {"enabled":true,"mode":"incremental","interval":360,"windows":[]},
// "price": {"enabled":true,"interval":30,"windows":[]},
// "status": {"enabled":true,"interval":60,"windows":[]}
//	}
//
// windows 元素 {"start":"09:00","end":"18:00"}；空 = 全天；start>end 视为跨午夜。

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplysynctask"
)

// 调度参数。
const (
	schedStaleAfter = 10 * time.Minute // 看门狗：心跳超时阈值
)

// scope 默认间隔（分钟；对齐 1.x DEFAULT_INTERVALS——scope 键存在但缺 interval 时用）。
var scopeDefaultInterval = map[string]int{
	ScopeCollect: 360,
	ScopePrice:   30,
	ScopeStatus:  60,
}

// timeWindow 执行时间窗（HH:MM；start>end 跨午夜）。
type timeWindow struct{ Start, End string }

// scopePlan 单 scope 计划。
type scopePlan struct {
	Enabled  bool
	Interval int    // 分钟
	Mode     string // collect 专用：full | incremental
	Windows  []timeWindow
}

// scheduleConfig schedule 整体配置。
type scheduleConfig struct {
	Enabled bool
	Scopes  map[string]scopePlan
}

// Scheduler 定时调度器（cron 入口 Scan / ReapStaleTasks）。
type Scheduler struct {
	repo *SupplyRepoImpl
	sync *SyncService
	log  *slog.Logger
}

// NewScheduler 构造。
func NewScheduler(repo *SupplyRepoImpl, sync *SyncService, log *slog.Logger) *Scheduler {
	return &Scheduler{repo: repo, sync: sync, log: log}
}

// Scan 每分钟扫描派发（cron；单轮耗时受连接数约束，失败仅日志不 panic）。
func (s *Scheduler) Scan(ctx context.Context) {
	conns, err := s.repo.ListActiveConnections(ctx)
	if err != nil {
		s.log.Warn("scheduler.list_failed", "err", err)
		return
	}
	for _, conn := range conns {
		cfg := loadScheduleConfig(conn)
		if !cfg.Enabled {
			continue
		}
		// 熔断冷却中：本轮跳过（到期后半开探测成功后自然恢复）
		if !conn.RateLimitUntil.IsZero() && time.Now().UTC().Before(conn.RateLimitUntil) {
			continue
		}
		if has, err := s.repo.HasRunningTask(ctx, conn.ID); err != nil {
			s.log.Warn("scheduler.has_running_failed", "connection_id", conn.ID, "err", err)
			continue
		} else if has {
			continue // 防重入：上轮任务未完结
		}
		for _, scope := range []string{ScopeCollect, ScopePrice, ScopeStatus} {
			if s.dispatchIfDue(ctx, conn, cfg.Scopes[scope], scope) {
				break // 每连接每轮至多派发一个（错峰；下轮继续）
			}
		}
	}
}

// dispatchIfDue 到期则派发；返回是否已派发。
func (s *Scheduler) dispatchIfDue(ctx context.Context, conn *ent.SupplyConnection, plan scopePlan, scope string) bool {
	if !plan.Enabled || plan.Interval <= 0 {
		return false
	}
	if !inWindows(plan.Windows, time.Now()) {
		return false
	}
	anchor := scopeAnchor(conn, scope)
	if !anchor.IsZero() && time.Since(anchor) < time.Duration(plan.Interval)*time.Minute {
		return false
	}
	mode := plan.Mode
	if mode == "" {
		mode = "incremental" // 首跑锚点为零 → 引擎自动全量
	}
	task, err := s.repo.CreateSyncTask(ctx, conn.ID, mode, scope, false)
	if err != nil {
		s.log.Warn("scheduler.create_task_failed", "connection_id", conn.ID, "scope", scope, "err", err)
		return false
	}
	if err := s.sync.StartTask(ctx, task.ID); err != nil {
		_ = s.repo.FinishTask(ctx, task.ID, supplysynctask.StatusFailed, "ENQUEUE_FAILED", err.Error())
		s.log.Warn("scheduler.enqueue_failed", "connection_id", conn.ID, "scope", scope, "err", err)
		return false
	}
	_ = s.repo.TouchScopeAnchor(ctx, conn.ID, scope)
	s.log.Info("scheduler.dispatched", "connection_id", conn.ID, "scope", scope, "mode", mode, "task_id", task.ID)
	return true
}

// ReapStaleTasks 看门狗：心跳超时 的 processing 任务置 failed（防僵死）。
func (s *Scheduler) ReapStaleTasks(ctx context.Context) {
	rows, err := s.repo.ListStaleProcessing(ctx, time.Now().UTC().Add(-schedStaleAfter))
	if err != nil {
		s.log.Warn("scheduler.reap_list_failed", "err", err)
		return
	}
	for _, t := range rows {
		reason := "任务心跳超时（>" + schedStaleAfter.String() + "），看门狗回收——可手动重跑"
		if err := s.repo.FinishTask(ctx, t.ID, supplysynctask.StatusFailed, "STALE_HEARTBEAT", reason); err != nil {
			s.log.Warn("scheduler.reap_failed", "task_id", t.ID, "err", err)
			continue
		}
		s.log.Warn("scheduler.reaped_stale_task", "task_id", t.ID, "connection_id", t.ConnectionID)
	}
}

// scopeAnchor scope → 调度锚点列（collect 兼容旧列 last_synced_at）。
func scopeAnchor(conn *ent.SupplyConnection, scope string) time.Time {
	switch scope {
	case ScopeCollect:
		if !conn.LastCollectAt.IsZero() {
			return conn.LastCollectAt
		}
		return conn.LastSyncedAt
	case ScopePrice:
		return conn.LastPriceSyncAt
	case ScopeStatus:
		return conn.LastStatusSyncAt
	}
	return time.Time{}
}

// loadScheduleConfig 解析 settings.schedule（结构不存在 = 定时调度关闭）。
func loadScheduleConfig(conn *ent.SupplyConnection) scheduleConfig {
	sched, _ := conn.Settings["schedule"].(map[string]any)
	cfg := scheduleConfig{Scopes: map[string]scopePlan{}}
	if sched == nil {
		return cfg
	}
	cfg.Enabled, _ = sched["enabled"].(bool)
	for _, scope := range []string{ScopeCollect, ScopePrice, ScopeStatus} {
		raw, ok := sched[scope].(map[string]any)
		if !ok {
			continue // scope 未配置 = 不启用（显式为准）
		}
		cfg.Scopes[scope] = parseScopePlan(raw, scope)
	}
	return cfg
}

// parseScopePlan 单 scope 配置归一化（interval 缺省用 scope 默认间隔）。
func parseScopePlan(raw map[string]any, scope string) scopePlan {
	plan := scopePlan{Mode: "incremental"}
	plan.Enabled, _ = raw["enabled"].(bool)
	switch v := raw["interval"].(type) {
	case float64:
		plan.Interval = int(v)
	case int:
		plan.Interval = v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			plan.Interval = n
		}
	}
	if plan.Interval <= 0 {
		plan.Interval = scopeDefaultInterval[scope]
	}
	if m, ok := raw["mode"].(string); ok && (m == "full" || m == "incremental") {
		plan.Mode = m
	}
	if ws, ok := raw["windows"].([]any); ok {
		for _, w := range ws {
			m, ok := w.(map[string]any)
			if !ok {
				continue
			}
			start, _ := m["start"].(string)
			end, _ := m["end"].(string)
			if start != "" && end != "" {
				plan.Windows = append(plan.Windows, timeWindow{Start: start, End: end})
			}
		}
	}
	return plan
}

// inWindows 当前时间是否落在任一窗口内（空 = 全天；start>end 跨午夜）。
func inWindows(windows []timeWindow, now time.Time) bool {
	if len(windows) == 0 {
		return true
	}
	minutes := now.Hour()*60 + now.Minute()
	for _, w := range windows {
		s, ok1 := parseHHMM(w.Start)
		e, ok2 := parseHHMM(w.End)
		if !ok1 || !ok2 {
			continue
		}
		if s <= e {
			if minutes >= s && minutes < e {
				return true
			}
			continue
		}
		// 跨午夜：22:00-06:00 = [22:00,24:00) ∪ [0:00,6:00)
		if minutes >= s || minutes < e {
			return true
		}
	}
	return false
}

// parseHHMM "HH:MM" → 当日分钟数。
func parseHHMM(s string) (int, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}
