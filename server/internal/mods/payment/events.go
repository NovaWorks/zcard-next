package payment

// payment 模块事件声明（附录 C）。
//
// 发布：
//   - payment.succeeded  支付成功（回调事务提交后，订阅者：fulfillment/procurement/
//                         affiliate/reseller/wallet/notify——异步展开，核心事务短小）
//   - payment.failed     支付失败
//   - refund.requested   退款发起（工单/管理员）
//   - refund.succeeded   退款成功（逆向：佣金扣回/分站利润扣回/库存回补/通知买家）
//
// 订阅：无（payment 是事件源头；order.paid 由 order 模块在 MarkPaid 时发布）。

import (
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

const (
	EventSucceeded       = events.PaymentSucceeded
	EventFailed          = events.PaymentFailed
	EventRefundRequested = events.RefundRequested
	EventRefundSucceeded = events.RefundSucceeded
)

// DedupeKey outbox dedupe_key（重复回调只发一次 payment.succeeded）。
func DedupeKey(paymentID uint64) string { return fmt.Sprintf("payment:%d:succeeded", paymentID) }
