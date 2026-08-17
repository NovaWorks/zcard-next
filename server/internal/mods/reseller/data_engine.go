package reseller

// 分账引擎（P3-04 T5 事件接线）：
//   订阅 order.paid → 按订单快照（subsite_profit/profit_eligible）入账
//   → SettleOrderProfit（幂等键 order_profit:<orderID>）→ 冻结 confirm_days 后可用。
// 防自购快照不产生利润：profit_eligible=false 直接 ACK 不入账。

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// paidPayload order.paid 载荷（与 order 模块发布结构对齐；分站快照随事件携带免回查）。
type settlePaidPayload struct {
	OrderID        uint64 `json:"order_id"`
	SubsiteID      uint64 `json:"subsite_id"`
	SubsiteProfit  int64  `json:"subsite_profit"`  // 分站利润快照（分；subsite_markup 合计）
	ProfitEligible bool   `json:"profit_eligible"` // 防自购快照（false = 不产生利润）
}

// SettleService 分站分账服务（order.paid 消费方；通道 C 异步、幂等）。
type SettleService struct {
	repo *ResellerRepo
	log  *slog.Logger
}

// NewSettleService 构造。
func NewSettleService(repo *ResellerRepo, logger *slog.Logger) *SettleService {
	return &SettleService{repo: repo, log: logger}
}

// OnOrderRefunded 订阅 order.refunded：按订单快照扣回分站利润
// （refund_deduct 负行；余额不足 → 负债态，后续利润优先抵扣）。
func (s *SettleService) OnOrderRefunded(ctx context.Context, env events.Envelope) error {
	var p struct {
		OrderID     uint64 `json:"order_id"`
		RefundRatio int64  `json:"refund_ratio"`
	}
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.OrderID == 0 {
		return nil // 载荷不合法：ACK 不重试
	}
	// 订单快照（subsite_profit 为利润基数；主站单/无利润单无动作）
	o, err := data.Client(ctx, s.repo.data).Order.Get(ctx, p.OrderID)
	if err != nil || o.SubsiteID == 0 || o.SubsiteProfit <= 0 {
		return nil
	}
	if err := s.repo.RefundDeduct(ctx, o.SubsiteID, o.ID, o.SubsiteProfit, p.RefundRatio); err != nil {
		s.log.Warn("reseller.refund_deduct_failed", "order_id", p.OrderID, "err", err)
	}
	return nil
}

// OnOrderPaid 订阅 order.paid：按订单快照分站利润入账（幂等 ACK）。
func (s *SettleService) OnOrderPaid(ctx context.Context, env events.Envelope) error {
	var p settlePaidPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return nil // 载荷不合法：ACK 不重试（order 侧契约破坏属异常路径）
	}
	if p.SubsiteID == 0 || !p.ProfitEligible || p.SubsiteProfit <= 0 {
		return nil // 主站单 / 自购快照 / 无利润：不产生分账
	}
	if err := s.repo.SettleOrderProfit(ctx, SettleInput{
		SubsiteID: p.SubsiteID,
		OrderID:   p.OrderID,
		Amount:    money.Cents(p.SubsiteProfit),
	}); err != nil {
		s.log.Warn("reseller.settle_failed", "order_id", p.OrderID, "err", err)
	}
	return nil
}
