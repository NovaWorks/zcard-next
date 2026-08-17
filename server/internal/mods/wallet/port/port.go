// Package port 为 wallet 模块对外契约（零依赖包）。
package port

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// Direction 流水方向（string(8)，与 ent schema 一致）。
const (
	DirectionIn  = "in"
	DirectionOut = "out"
)

// Entry 账务入账请求（一切余额变动的唯一入口 InTx 的参数）。
type Entry struct {
	UserID    uint64
	Direction string
	Type      string // order_pay/order_refund/recharge/commission/adjust...
	Amount    money.Cents
	Reference string // 幂等键（重复提交直接返回成功，§5.6）
	OrderID   uint64
	Operator  uint64
	Remark    string
}

// PointEntry 积分入账请求（与 Entry 同构）。
type PointEntry struct {
	UserID    uint64
	Direction string // in | out
	Type      string // earn_recharge / earn_consume / redeem / adjust ...
	Amount    int64  // 正数
	Reference string // 幂等键
	OrderID   uint64
	Remark    string
}

// Points 积分账本窄接口（payment 充值赠送消费，通道 B 同事务）。
type Points interface {
	PointCreditInTx(ctx context.Context, e PointEntry) error
}

// Wallet 钱包窄接口（order 余额支付 / payment 退款 / affiliate 佣金入账消费，通道 B 同事务）。
type Wallet interface {
	// CreditInTx 入账（充值/退款回/佣金）；幂等重入：reference 已存在直接返回成功。
	CreditInTx(ctx context.Context, e Entry) error
	// DebitInTx 扣款（余额支付下单）；余额不足返回 ErrInsufficientBalance，订单不创建。
	DebitInTx(ctx context.Context, e Entry) error
	// Lock / Unlock 冻结流转（佣金/分站利润冻结期、提现申请）。
	Lock(ctx context.Context, userID uint64, amount money.Cents, availableAt int64) error
	Unlock(ctx context.Context, userID uint64, amount money.Cents) error
}
