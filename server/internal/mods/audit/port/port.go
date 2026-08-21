// Package port 为 audit 模块对外契约（零依赖包）。
package port

import (
	"context"
	"net"
	"strings"
	"time"
)

// SecurityEntry 安全审计条目（埋点入参）。
type SecurityEntry struct {
	ActorType string // admin | user | guest | system
	ActorID   uint64
	Action    string // 事件名（identity.login_failed / card.decrypt / ...）
	IP        string
	Metadata  map[string]any // 关键 ID（明文卡密/凭据绝不入内）
}

// Auditor 安全审计端口（identity/inventory/authz 等模块消费，通道 A）。
// 实现纪律：写失败不阻断业务（1.x SecurityAudit 纪律）。
type Auditor interface {
	Security(ctx context.Context, e SecurityEntry)
}

// GateInput 风控闸门入参。
type GateInput struct {
	RiskIP string // 规范化 IP（IPv6 已按 /64 聚合）
	UserID uint64 // 0 = 游客
	// FetchFail 取货失败锁定键（非空时走失败计数锁定而非下单闸门）
	FetchFailKey string
}

// RiskGate 风控闸门端口（order 下单 / fulfillment 取货消费，通道 A）。
type RiskGate interface {
	// Check 下单前闸门：IP 黑名单 → pending 闸门 → 频率限流（事务内复查防穿透）。
	Check(ctx context.Context, in GateInput) error
	// LockFetchFailure 取货连续失败锁定（N 次锁 IP+订单组合，TTL 解锁）。
	LockFetchFailure(ctx context.Context, key string) error
	// IsLocked 锁定检查。
	IsLocked(ctx context.Context, key string) (bool, error)
}

// NormalizeIP IP 规范化：IPv4 原样；IPv6 按 /64 聚合（orders.risk_ip 写入口统一）。
func NormalizeIP(ip string) string {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return ip
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	masked := parsed.Mask(net.CIDRMask(64, 128))
	return masked.String()
}

// TrafficDay 单日流量（PV/UV）。
type TrafficDay struct {
	Date string
	PV   int64
	UV   int64
}

// TrafficReader 访问统计读取端口（dashboard 工作台消费，通道 A）。
type TrafficReader interface {
	// CountOnlineUsers 在线用户数（最近 since 内有活跃心跳；分站隔离）。
	CountOnlineUsers(ctx context.Context, subsite uint64, since time.Time) (int64, error)
	// TrafficByDay 近 N 天 PV/UV（不含缺日补零——调用方补齐日期序列）。
	TrafficByDay(ctx context.Context, subsite uint64, days int) ([]TrafficDay, error)
}
