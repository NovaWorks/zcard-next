package payment

// P1-03 慢通道顺延探测：订单超时取消前查 pending 流水——usdt 族链上确认
// 慢于订单 TTL（1.x 误杀教训），存在则超时任务顺延该单不关闭。
// 名单硬编码（任务书 T2 第 4 条口径）；新慢驱动接入时在此扩表。

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/paymentchannel"
)

// slowDrivers 慢支付渠道驱动名单（usdt 族）。
var slowDrivers = map[string]bool{
	"epusdt": true,
	"usdt":   true,
}

// HasPendingSlowPayment 订单是否存在慢通道 pending 流水（P1-03 T6 顺延判据）。
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
	codes := make([]string, 0, len(pays))
	for _, p := range pays {
		codes = append(codes, p.Channel)
	}
	channels, err := client.PaymentChannel.Query().
		Where(paymentchannel.CodeIn(codes...)).
		All(ctx)
	if err != nil {
		return false, err
	}
	for _, ch := range channels {
		if slowDrivers[ch.Driver] {
			return true, nil
		}
	}
	return false, nil
}
