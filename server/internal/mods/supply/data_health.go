package supply

// 货源连接健康度（P2-01 T5）：Ping 探活（更新 last_ping_at/last_ping_ok/
// balance_cache + settings.ping_history 累计统计）、健康列表（成功率/平均延迟）。
// 累计统计是 M4 供应商评分的基础数据（任务书：从现在记）。

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

// PingConnection 探活单个连接（API 手动触发 + cron 周期探活共用）。
// 凭据解密失败 → 返回明确错误（提示重配），不中断其余连接。
func (s *SyncService) PingConnection(ctx context.Context, connectionID uint64) (*adapter.PingResult, error) {
	conn, err := s.repo.GetConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	return s.pingOne(ctx, conn)
}

func (s *SyncService) pingOne(ctx context.Context, conn *ent.SupplyConnection) (*adapter.PingResult, error) {
	credsJSON, err := s.repo.OpenCredentials(conn)
	if err != nil {
		_ = s.repo.UpdatePingResult(ctx, conn.ID, false, 0, 0, err.Error())
		return nil, err
	}
	var creds adapter.Credentials
	if err := json.Unmarshal([]byte(credsJSON), &creds); err != nil {
		_ = s.repo.UpdatePingResult(ctx, conn.ID, false, 0, 0, "凭据结构不合法: "+err.Error())
		return nil, err
	}
	a, err := adapter.New(conn.Driver, conn.BaseURL, creds, parseRetryIntervals(conn.RetryIntervals))
	if err != nil {
		_ = s.repo.UpdatePingResult(ctx, conn.ID, false, 0, 0, err.Error())
		return nil, err
	}
	start := time.Now()
	res, err := a.Ping(ctx)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		_ = s.repo.UpdatePingResult(ctx, conn.ID, false, latency, 0, err.Error())
		return nil, err
	}
	balance := int64(-1)
	if res != nil {
		balance = res.Balance
	}
	_ = s.repo.UpdatePingResult(ctx, conn.ID, true, latency, balance, "")
	return res, nil
}

// HealthItem 连接健康快照（ListHealth 输出）。
type HealthItem struct {
	ConnectionID    uint64
	Name            string
	Driver          string
	Status          string
	LastPingOK      bool
	LastPingAt      int64
	LastSyncedAt    int64
	LastError       string
	BalanceCache    int64
	PingSuccessRate int64 // 千分比（0-1000）
	AvgLatencyMs    int64
}

// ListHealth 全量健康列表（含 disabled；统计自 settings.ping_history）。
func (s *SyncService) ListHealth(ctx context.Context) ([]HealthItem, error) {
	conns, _, err := s.repo.ListConnections(ctx, 1, 100000)
	if err != nil {
		return nil, err
	}
	out := make([]HealthItem, 0, len(conns))
	for _, c := range conns {
		item := HealthItem{
			ConnectionID: c.ID,
			Name:         c.Name,
			Driver:       c.Driver,
			Status:       string(c.Status),
			LastPingOK:   c.LastPingOk,
			BalanceCache: c.BalanceCache,
			LastError:    c.LastError,
		}
		if !c.LastPingAt.IsZero() {
			item.LastPingAt = c.LastPingAt.Unix()
		}
		if !c.LastSyncedAt.IsZero() {
			item.LastSyncedAt = c.LastSyncedAt.Unix()
		}
		if hist, ok := c.Settings["ping_history"].(map[string]any); ok {
			okN := toInt64(hist["ok"])
			failN := toInt64(hist["fail"])
			totalLatency := toInt64(hist["total_latency_ms"])
			samples := okN + failN
			if samples > 0 {
				item.PingSuccessRate = okN * 1000 / samples
				item.AvgLatencyMs = totalLatency / samples
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// PingAllActive 周期探活全部 active 连接（cron 注册；单连接失败不阻断）。
func (s *SyncService) PingAllActive(ctx context.Context) {
	conns, err := s.repo.ListActiveConnections(ctx)
	if err != nil {
		s.log.Warn("supply.health.list_active_failed", "err", err)
		return
	}
	for _, c := range conns {
		ctx2, cancel := context.WithTimeout(ctx, 20*time.Second)
		if _, err := s.pingOne(ctx2, c); err != nil {
			s.log.Warn("supply.health.ping_failed", "connection_id", c.ID, "err", fmt.Sprintf("%v", err))
		}
		cancel()
	}
}
