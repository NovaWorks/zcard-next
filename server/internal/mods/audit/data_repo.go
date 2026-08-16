package audit

// 数据仓储（P2-06）：四表 CRUD + 风控闸门。
// 纪律：审计写失败不阻断业务（1.x SecurityAudit）——所有写方法错误仅日志。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/auditlog"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/risklockkey"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/securityauditlog"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/visitlog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/audit/port"
)

// 闸门阈值（settings.security 可覆盖——读取侧 M3 接线；默认值与文档一致）。
const (
	DefaultMaxPendingPerIP   = 3
	DefaultFetchFailLockN    = 5
	DefaultFetchLockTTL      = 30 * time.Minute
	DefaultOrderPerMinPerIP  = 10
)

// 哨兵错误。
var (
	ErrIPBlacklisted  = errors.New("audit: IP 已被列入黑名单")
	ErrPendingExceed  = errors.New("audit: 同 IP 待支付订单超限")
	ErrFreqExceed     = errors.New("audit: 下单频率超限")
	ErrFetchLocked    = errors.New("audit: 取货失败锁定中（稍后再试）")
)

// AuditRepo 审计仓储。
type AuditRepo struct {
	data *data.Data
	log  *slog.Logger

	// 黑名单进程内缓存（admin 变更失效；解析失败用空名单 fail-open）
	blacklistMu  sync.RWMutex
	blacklist    *ipBlacklist
	blacklistRaw []string

	// 下单频率窗口（进程内滑动窗口；多实例由 pending 闸门 DB 复查兜底）
	freqMu  sync.Mutex
	freqWin map[string][]time.Time
}

// NewAuditRepo 构造。
func NewAuditRepo(d *data.Data, logger *slog.Logger) *AuditRepo {
	return &AuditRepo{data: d, log: logger, freqWin: map[string][]time.Time{}}
}

// ── T2 操作审计 ───────────────────────────────────────────

// OpLogInput 操作审计入参。
type OpLogInput struct {
	OperatorType    string // admin | user | system
	OperatorID      uint64
	PermissionPoint string
	Action          string // POST | PUT | DELETE
	Route           string
	Before          map[string]any
	After           map[string]any
	IP              string
	UserAgent       string
}

// WriteOpLog 落操作审计（失败仅日志——不阻断业务）。
func (r *AuditRepo) WriteOpLog(ctx context.Context, in OpLogInput) error {
	create := data.Client(ctx, r.data).AuditLog.Create().
		SetOperatorType(auditlog.OperatorType(in.OperatorType)).
		SetAction(in.Action).
		SetRoute(in.Route)
	if in.OperatorID > 0 {
		create.SetOperatorID(in.OperatorID)
	}
	if in.PermissionPoint != "" {
		create.SetPermissionPoint(in.PermissionPoint)
	}
	if in.Before != nil {
		create.SetBefore(in.Before)
	}
	if in.After != nil {
		create.SetAfter(in.After)
	}
	if in.IP != "" {
		create.SetIP(in.IP)
	}
	if in.UserAgent != "" {
		create.SetUserAgent(in.UserAgent)
	}
	_, err := create.Save(ctx)
	if err != nil {
		r.log.Warn("audit.op_write_failed", "route", in.Route, "err", err)
	}
	return nil
}

// ListOpLogs 操作审计查询。
func (r *AuditRepo) ListOpLogs(ctx context.Context, operatorID uint64, page, size int) ([]*ent.AuditLog, int, error) {
	q := data.Client(ctx, r.data).AuditLog.Query().Order(ent.Desc(auditlog.FieldID))
	if operatorID > 0 {
		q = q.Where(auditlog.OperatorID(operatorID))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// ── T3 安全审计（port.Auditor 实现）──────────────────────

// Security 安全事件埋点（写失败不阻断——1.x 纪律）。
func (r *AuditRepo) Security(ctx context.Context, e port.SecurityEntry) {
	create := data.Client(ctx, r.data).SecurityAuditLog.Create().
		SetActorType(securityauditlog.ActorType(orDefault(e.ActorType, "system"))).
		SetAction(e.Action)
	if e.ActorID > 0 {
		create.SetActorID(e.ActorID)
	}
	if e.IP != "" {
		create.SetIP(e.IP)
	}
	if e.Metadata != nil {
		create.SetMetadata(e.Metadata)
	}
	if _, err := create.Save(ctx); err != nil {
		r.log.Warn("audit.security_write_failed", "action", e.Action, "err", err)
	}
}

// ListSecurityLogs 安全审计查询。
func (r *AuditRepo) ListSecurityLogs(ctx context.Context, action string, page, size int) ([]*ent.SecurityAuditLog, int, error) {
	q := data.Client(ctx, r.data).SecurityAuditLog.Query().Order(ent.Desc(securityauditlog.FieldID))
	if action != "" {
		q = q.Where(securityauditlog.ActionContains(action))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// ── T4 风控闸门（port.RiskGate 实现）──────────────────────

// SetBlacklist 更新黑名单缓存（admin CRUD 后调用）。
func (r *AuditRepo) SetBlacklist(entries []string) {
	r.blacklistMu.Lock()
	r.blacklistRaw = entries
	r.blacklist = parseBlacklist(entries)
	r.blacklistMu.Unlock()
}

// BlacklistRaw 当前黑名单配置。
func (r *AuditRepo) BlacklistRaw() []string {
	r.blacklistMu.RLock()
	defer r.blacklistMu.RUnlock()
	return r.blacklistRaw
}

// Check 下单闸门：黑名单 → pending 数（事务内复查语义：本方法在下单事务内调用）→ 频率。
func (r *AuditRepo) Check(ctx context.Context, in port.GateInput) error {
	ip := port.NormalizeIP(in.RiskIP)

	// 1) 黑名单
	r.blacklistMu.RLock()
	bl := r.blacklist
	r.blacklistMu.RUnlock()
	if bl.contains(ip) {
		return ErrIPBlacklisted
	}

	// 2) pending 闸门（DB 复查——并发 50 单同 IP 不穿透：计数查询在同一事务，
	// order 模块在下单事务内调用本方法）
	count, err := data.Client(ctx, r.data).Order.Query().
		Where(order.RiskIPEQ(ip), order.StatusEQ(order.StatusPendingPayment)).
		Count(ctx)
	if err != nil {
		return nil // 查询失败 fail-open（可用性优先；黑名单仍生效）
	}
	if count >= DefaultMaxPendingPerIP {
		return ErrPendingExceed
	}

	// 3) 频率限流（进程内滑动窗口）
	if r.freqExceed(ip) {
		return ErrFreqExceed
	}
	return nil
}

// freqExceed 滑动窗口频率检查（每 IP 每分钟）。
func (r *AuditRepo) freqExceed(ip string) bool {
	now := time.Now()
	r.freqMu.Lock()
	defer r.freqMu.Unlock()
	win := r.freqWin[ip]
	kept := win[:0]
	for _, t := range win {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	if len(kept) >= DefaultOrderPerMinPerIP {
		r.freqWin[ip] = kept
		return true
	}
	r.freqWin[ip] = append(kept, now)
	return false
}

// LockFetchFailure 取货失败锁定（连续 N 次锁 IP+订单组合；risk_lock_keys TTL）。
func (r *AuditRepo) LockFetchFailure(ctx context.Context, key string) error {
	hash := hashKey(key)
	_, err := data.Client(ctx, r.data).RiskLockKey.Create().
		SetKeyHash(hash).
		SetExpiresAt(time.Now().UTC().Add(DefaultFetchLockTTL)).
		Save(ctx)
	if err != nil {
		return nil // 锁定写失败不阻断（fail-open；计数侧仍防暴力）
	}
	r.Security(ctx, port.SecurityEntry{
		ActorType: "guest", Action: "fetch.locked",
		Metadata: map[string]any{"key_prefix": prefixOf(key)},
	})
	return nil
}

// IsLocked 锁定检查（TTL 过期视为未锁）。
func (r *AuditRepo) IsLocked(ctx context.Context, key string) (bool, error) {
	n, err := data.Client(ctx, r.data).RiskLockKey.Query().
		Where(risklockkey.KeyHash(hashKey(key)), risklockkey.ExpiresAtGT(time.Now().UTC())).
		Count(ctx)
	if err != nil {
		return false, nil
	}
	return n > 0, nil
}

// CleanupExpiredLocks 过期锁清理（cron）。
func (r *AuditRepo) CleanupExpiredLocks(ctx context.Context) error {
	_, err := data.Client(ctx, r.data).RiskLockKey.Delete().
		Where(risklockkey.ExpiresAtLT(time.Now().UTC())).
		Exec(ctx)
	return err
}

// ── T5 访问统计 ───────────────────────────────────────────

// VisitCounter 进程内聚合计数器（批量落库——不逐请求写）。
type VisitCounter struct {
	mu    sync.Mutex
	rows  map[string]*visitRow // key: date|hour|path
	total int64
	r     *AuditRepo
}

type visitRow struct {
	subsiteID uint64
	date      string
	hour      int8
	path      string
	pv        int64
}

// NewVisitCounter 构造。
func NewVisitCounter() *VisitCounter { return &VisitCounter{rows: map[string]*visitRow{}} }

// Record 记一次访问（内存）。
func (c *VisitCounter) Record(subsiteID uint64, path string) {
	now := time.Now().UTC()
	key := fmt.Sprintf("%s|%d|%s", now.Format("20060102"), now.Hour(), path)
	c.mu.Lock()
	defer c.mu.Unlock()
	row, ok := c.rows[key]
	if !ok {
		row = &visitRow{subsiteID: subsiteID, date: now.Format("20060102"), hour: int8(now.Hour()), path: path}
		c.rows[key] = row
	}
	row.pv++
	c.total++
	// 聚合阈值批量落库
	if c.total >= 100 {
		c.flushLocked()
	}
}

// Bind 绑定仓储（wire 装配后调用一次）。
func (c *VisitCounter) Bind(r *AuditRepo) { c.r = r }

// Flush 强制落库（cron 周期调用兜底）。
func (c *VisitCounter) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.flushLocked()
}

// flushLocked 落库（调用方持锁）。使用背景上下文（计数线程无请求上下文）。
func (c *VisitCounter) flushLocked() {
	if c.r == nil || len(c.rows) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := data.Client(ctx, c.r.data)
	for _, row := range c.rows {
		// UPSERT：UNIQUE(subsite_id, stat_date, stat_hour, path) 命中则累加
		existing, err := client.VisitLog.Query().
			Where(
				visitlog.SubsiteID(row.subsiteID),
				visitlog.StatDate(row.date),
				visitlog.StatHour(row.hour),
				visitlog.Path(row.path),
			).
			Only(ctx)
		if err == nil {
			if _, err := client.VisitLog.UpdateOneID(existing.ID).
				AddPv(row.pv).
				Save(ctx); err == nil {
				continue
			}
		}
		_, _ = client.VisitLog.Create().
			SetSubsiteID(row.subsiteID).
			SetStatDate(row.date).
			SetStatHour(row.hour).
			SetPath(row.path).
			SetPv(row.pv).
			SetUv(row.pv). // 轻量口径：无会话标识时 uv≈pv（M3 细化）
			Save(ctx)
	}
	c.rows = map[string]*visitRow{}
	c.total = 0
}

// ── 工具 ──────────────────────────────────────────────────

func hashKey(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func prefixOf(s string) string {
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// ListVisitStats 访问统计查询。
func (r *AuditRepo) ListVisitStats(ctx context.Context, statDate string, page, size int) ([]*ent.VisitLog, int, error) {
	q := data.Client(ctx, r.data).VisitLog.Query().
		Where(visitlog.StatDate(statDate)).
		Order(ent.Desc(visitlog.FieldPv))
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// securityEntryOf 快捷构造。
func securityEntryOf(action string, metadata map[string]any) port.SecurityEntry {
	return port.SecurityEntry{ActorType: "admin", Action: action, Metadata: metadata}
}
