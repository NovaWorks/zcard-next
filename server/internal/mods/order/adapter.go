package order

// OrderLifecycle 适配器（§4.6 破环点：payment 回调事务内经窄接口推进订单，
// 同事务内调 OrderUsecase——状态机 CAS + 状态事件 + outbox(order.paid) 一次性完成）。
// bootstrap 装配：payment 消费本端口，wire 经 ProvideOrderLifecycle 注入。

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/mods/order/port"
)

// lifecycleAdapter OrderUsecase → port.OrderLifecycle。
type lifecycleAdapter struct {
	uc *OrderUsecase
}

// MarkPaid 支付成功推进（幂等：已 paid 直接成功；发布 order.paid 事件）。
func (a lifecycleAdapter) MarkPaid(ctx context.Context, fact port.PaidFact) error {
	return a.uc.MarkPaid(ctx, fact.OrderNo)
}

// Cancel 取消订单（状态机校验 + 释放锁卡 + 券返还）。
func (a lifecycleAdapter) Cancel(ctx context.Context, orderNo, reason string, op port.Operator) error {
	return a.uc.CancelOrder(ctx, orderNo, reason, op.Type, op.ID)
}

// ProvideOrderLifecycle wire provider（返回端口接口，payment 模块消费）。
func ProvideOrderLifecycle(uc *OrderUsecase) port.OrderLifecycle {
	return lifecycleAdapter{uc: uc}
}
