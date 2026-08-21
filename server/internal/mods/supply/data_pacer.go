package supply

// 自适应节奏器（P2-10 S2）：连接级 AIMD——遇限流乘性降速、连续成功加性回升。
//
//   事件                                    状态迁移
//   ────────────────────────────────────────────────────────────────────
//   429 / WAF 页（ErrRateLimited）          currentDelay ×2（封顶 60s）
//   连续 2 次限流事件                        渠道熔断：rate_limit_until = now +
//                                           cooldown（5min 起，每次熔断 ×2，
//                                           封顶 60min；写 last_error + 事件）
//   连续 20 次成功                           currentDelay ×0.9（回落至配置底线
//                                           schedule.request_delay 为止）
//   熔断到期                                 半开：放行一个探测请求；成功 → 解除
//                                           （冷却时长归零重来）；再限流 → 冷却翻倍
//
// 消费方：SyncService（页间动态节流）与 Gateway（采购出站熔断判定）。
// 持久化：rate_state JSON（current_delay_ms / success_streak / blocked_count /
// cooldown_sec）——重启后降速保持、熔断冷却由 rate_limit_until 列判定；
// 连续限流计数与半开标志仅内存态（崩溃丢失可接受：重新积累两次才熔断，偏保守）。

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

// 节奏器参数（对齐计划 §5.3；不可配——参数面已足够，避免过度配置）。
const (
	pacerMaxDelay      = 60 * time.Second // 降速封顶
	pacerCooldownBase  = 5 * time.Minute  // 首次熔断冷却
	pacerCooldownMax   = time.Hour        // 冷却封顶
	pacerTripThreshold = 2                // 连续限流 N 次 → 熔断
	pacerRecoverStreak = 20               // 连续成功 N 次 → 间隔回落 10%
	pacerRecoverFactor = 0.9
)

// pacerState 持久化状态（rate_state JSON 的结构）。
type pacerState struct {
	CurrentDelayMs int64 `json:"current_delay_ms"`
	SuccessStreak  int   `json:"success_streak"`
	BlockedCount   int64 `json:"blocked_count"`
	CooldownSec    int64 `json:"cooldown_sec"`
}

// 内存补充态（不持久化）。
type pacerMem struct {
	state         pacerState
	consecutiveRL int  // 连续限流事件（熔断判据）
	halfOpen      bool // 熔断到期后已放行探测（等待成败反馈）
	lastPersisted pacerState
}

// Pacer 自适应节奏器（wire 单例；状态按连接隔离）。
type Pacer struct {
	repo   *SupplyRepoImpl
	outbox events.Writer
	log    *slog.Logger
	mu     sync.Mutex
	mem    map[uint64]*pacerMem
}

// NewPacer 构造。
func NewPacer(repo *SupplyRepoImpl, outbox events.Writer, log *slog.Logger) *Pacer {
	return &Pacer{repo: repo, outbox: outbox, log: log, mem: map[uint64]*pacerMem{}}
}

// load 连接的内存态（懒加载：首次从 rate_state 列恢复）。
func (p *Pacer) load(conn *ent.SupplyConnection) *pacerMem {
	m, ok := p.mem[conn.ID]
	if ok {
		return m
	}
	m = &pacerMem{}
	if raw, err := json.Marshal(conn.RateState); err == nil && len(conn.RateState) > 0 {
		_ = json.Unmarshal(raw, &m.state)
	}
	m.lastPersisted = m.state
	p.mem[conn.ID] = m
	return m
}

// Delay 出站前应等待的间隔：max(自适应当前间隔, schedule.request_delay 底线)。
func (p *Pacer) Delay(conn *ent.SupplyConnection) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := p.load(conn)
	base := loadScheduleSettings(conn).PageDelay
	if d := time.Duration(m.state.CurrentDelayMs) * time.Millisecond; d > base {
		return d
	}
	return base
}

// OnSuccess 出站成功反馈：半开探测成功 → 解除熔断（冷却归零）；否则连续成功
// 计数，每 20 次间隔回落 10%（至底线为止）。持久化仅在有变化时写库。
func (p *Pacer) OnSuccess(ctx context.Context, conn *ent.SupplyConnection) {
	p.mu.Lock()
	m := p.load(conn)
	m.consecutiveRL = 0
	if m.halfOpen {
		m.halfOpen = false
		m.state.CooldownSec = 0 // 解除熔断：下次从基础冷却重新计
		p.mu.Unlock()
		_ = p.repo.ClearRateLimit(ctx, conn.ID)
		p.log.Info("pacer.recovered", "connection_id", conn.ID)
		return
	}
	m.state.SuccessStreak++
	if m.state.SuccessStreak >= pacerRecoverStreak && m.state.CurrentDelayMs > 0 {
		m.state.SuccessStreak = 0
		base := loadScheduleSettings(conn).PageDelay.Milliseconds()
		next := int64(float64(m.state.CurrentDelayMs) * pacerRecoverFactor)
		if next <= base {
			next = 0 // 回落到底线（0 = 用配置值）
		}
		m.state.CurrentDelayMs = next
	}
	p.persistIfChanged(ctx, conn.ID, m)
	p.mu.Unlock()
}

// OnRateLimited 限流反馈：间隔倍增（封顶 60s）；连续 2 次 → 熔断（冷却指数递增）。
// 返回 tripped 表示本次触发了熔断。
func (p *Pacer) OnRateLimited(ctx context.Context, conn *ent.SupplyConnection, reason string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := p.load(conn)
	// 间隔倍增
	d := time.Duration(m.state.CurrentDelayMs) * time.Millisecond
	if d <= 0 {
		d = loadScheduleSettings(conn).PageDelay
	}
	d *= 2
	if d > pacerMaxDelay {
		d = pacerMaxDelay
	}
	m.state.CurrentDelayMs = d.Milliseconds()
	m.state.SuccessStreak = 0
	m.consecutiveRL++
	// 半开中再限流：冷却翻倍（不重置 consecutiveRL 计数——直接再熔断）
	if m.consecutiveRL >= pacerTripThreshold || m.halfOpen {
		cooldown := time.Duration(m.state.CooldownSec) * time.Second
		if cooldown <= 0 {
			cooldown = pacerCooldownBase
		} else {
			cooldown *= 2
			if cooldown > pacerCooldownMax {
				cooldown = pacerCooldownMax
			}
		}
		m.state.CooldownSec = int64(cooldown.Seconds())
		m.state.BlockedCount++
		m.halfOpen = false
		m.consecutiveRL = 0
		until := time.Now().UTC().Add(cooldown)
		_ = p.repo.TripRateLimit(ctx, conn.ID, until, m.state.toMap(), "上游限流熔断至 "+until.Local().Format("2006-01-02 15:04:05")+"："+reason)
		p.publishRateLimited(ctx, conn.ID, until, reason)
		p.log.Warn("pacer.tripped", "connection_id", conn.ID, "cooldown", cooldown, "reason", reason)
		return true
	}
	p.persistIfChanged(ctx, conn.ID, m)
	return false
}

// CooldownActive 熔断冷却判定：未到期 → true；到期 → 标记半开（放行一个探测）
// 并清内存期截止（DB 列由探测成败的 OnSuccess/OnRateLimited 收尾）。
func (p *Pacer) CooldownActive(conn *ent.SupplyConnection) bool {
	if conn.RateLimitUntil.IsZero() {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().UTC().Before(conn.RateLimitUntil) {
		return true
	}
	m := p.load(conn)
	if !m.halfOpen {
		m.halfOpen = true
		p.log.Info("pacer.half_open_probe", "connection_id", conn.ID)
	}
	return false
}

// Reset 重置节奏器（管理界面按钮）：清内存与持久状态、解除熔断。
func (p *Pacer) Reset(ctx context.Context, connID uint64) error {
	p.mu.Lock()
	delete(p.mem, connID)
	p.mu.Unlock()
	if err := p.repo.UpdateRateState(ctx, connID, map[string]any{}); err != nil {
		return err
	}
	return p.repo.ClearRateLimit(ctx, connID)
}

// PacerSnapshot 节奏器状态快照（管理界面展示）。
type PacerSnapshot struct {
	CurrentDelay  time.Duration // 当前自适应间隔（0 = 用配置底线）
	SuccessStreak int
	BlockedCount  int64
	CooldownUntil time.Time // 熔断截止（零值 = 未熔断）
	CooldownSec   int64     // 下次熔断将使用的冷却时长
	HalfOpen      bool
}

// Snapshot 读取状态快照。
func (p *Pacer) Snapshot(conn *ent.SupplyConnection) PacerSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := p.load(conn)
	return PacerSnapshot{
		CurrentDelay:  time.Duration(m.state.CurrentDelayMs) * time.Millisecond,
		SuccessStreak: m.state.SuccessStreak,
		BlockedCount:  m.state.BlockedCount,
		CooldownUntil: conn.RateLimitUntil,
		CooldownSec:   m.state.CooldownSec,
		HalfOpen:      m.halfOpen,
	}
}

// persistIfChanged 状态有变化才写库（成功计数高频变化不落盘——streak 只在内存）。
// 调用方持锁。
func (p *Pacer) persistIfChanged(ctx context.Context, connID uint64, m *pacerMem) {
	if m.state.CurrentDelayMs == m.lastPersisted.CurrentDelayMs &&
		m.state.BlockedCount == m.lastPersisted.BlockedCount &&
		m.state.CooldownSec == m.lastPersisted.CooldownSec {
		return
	}
	m.lastPersisted = m.state
	if err := p.repo.UpdateRateState(ctx, connID, m.state.toMap()); err != nil {
		p.log.Warn("pacer.persist_failed", "connection_id", connID, "err", err)
	}
}

// publishRateLimited 熔断告警事件（P2-05 告警/界面红点数据源）。
func (p *Pacer) publishRateLimited(ctx context.Context, connID uint64, until time.Time, reason string) {
	if p.outbox == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"connection_id": connID, "cooldown_until": until.Unix(), "reason": reason,
	})
	if err != nil {
		return
	}
	agg := "supply_conn:" + strconv.FormatUint(connID, 10)
	_ = p.outbox.Write(ctx, "supply", events.SupplyRateLimited, agg, agg, payload)
}

// toMap / fromMap：ent JSON 列是 map[string]any（SuccessStreak 序列化后是 float64）。
func (s pacerState) toMap() map[string]any {
	return map[string]any{
		"current_delay_ms": s.CurrentDelayMs,
		"success_streak":   s.SuccessStreak,
		"blocked_count":    s.BlockedCount,
		"cooldown_sec":     s.CooldownSec,
	}
}
