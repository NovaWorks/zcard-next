package fulfillment

// 事件消费（P1-06 M1b 接线）：order.paid → 自动交付。
// 与 procurement/affiliate/reseller/notify 同款消费形态——outbox relay 投递，幂等由
// FulfillOrder（已交付直接返回）兜底。

import (
	"context"
	"encoding/json"

	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

// OnOrderPaid 订阅 order.paid：解析订单号 → FulfillOrder（自动交付）。
func (r *DeliveryRepoImpl) OnOrderPaid(ctx context.Context, env events.Envelope) error {
	var p struct {
		OrderNo string `json:"order_no"`
	}
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.OrderNo == "" {
		return nil // 载荷不合法：ACK 不重试（order 侧契约破坏属异常路径）
	}
	return r.FulfillOrder(ctx, p.OrderNo)
}
