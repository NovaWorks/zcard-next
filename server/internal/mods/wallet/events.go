package wallet

// wallet 模块事件声明（附录 C）。
//
// 发布：
//   - recharge.succeeded  充值成功（赠送计算后）
//   - withdrawal.reviewed 提现审核完成（M3）
//
// 订阅：
//   - payment.succeeded(type=recharge) → 充值入账 + 赠送（幂等键 recharge:<payID>）
//   - order.refunded → 钱包退款逆向入账（幂等键 order_refund:<id>:<seq>）

import "github.com/NovaWorks/zcard-next/server/internal/platform/events"

const (
	EventRechargeSucceeded  = events.RechargeSucceeded
	EventWithdrawalReviewed = events.WithdrawalReviewed
)
