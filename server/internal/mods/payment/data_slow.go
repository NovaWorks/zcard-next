package payment

// 慢通道顺延探测：订单超时取消前查 pending 流水——usdt 族链上确认
// 慢于订单 TTL（1.x 误杀教训），存在则超时任务顺延该单不关闭。
// 名单硬编码（ 第 4 条口径）；新慢驱动接入时在此扩表。

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
)

// slowDrivers 慢支付渠道驱动名单（usdt 族）。
var slowDrivers = map[string]bool{
	"epusdt": true,
	"usdt":   true,
}

// HasPendingSlowPayment 订单是否存在慢通道 pending 流水（ 顺延判据）。
func (r *PaymentRepoImpl) HasPendingSlowPayment(ctx context.Context, orderID uint64) (bool, error) {
	client := data.Client(ctx, r.data)
	pays, err := client.Payment.Query().
		Where(payment.OrderID(orderID), payment.StatusEQ(payment.StatusPending)).
		All(ctx)
	if err != nil {
		return false, err
	}
	if len(pays) == 0 {
		return false, nil
	}
	for _, p := range pays {
		driver := p.DriverSnapshot
		if driver == "" {
			ch, err := r.channelForPayment(ctx, p)
			if err != nil {
				return false, err
			}
			driver = ch.Driver
		}
		if slowDrivers[driver] {
			return true, nil
		}
	}
	return false, nil
}
