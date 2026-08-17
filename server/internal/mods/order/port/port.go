// Package port 为 order 模块对外契约（零依赖包）。
package port

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// PaidFact 支付成功事实（payment 回调事务内经 OrderLifecycle 推进订单，
// §4.6 破环点：payment → order 窄接口回调，bootstrap 注入）。
type PaidFact struct {
	OrderNo        string
	PaymentID      uint64
	Channel        string
	Amount         money.Cents // 实收（分，基础货币口径核对）
	ChannelOrderNo string
}

// OrderLifecycle 订单生命周期窄接口（payment 消费，通道 B 同事务）。
type OrderLifecycle interface {
	// MarkPaid 订单置 paid：状态机校验 → 锁卡转售出预留 → 落 order_status_events
	// → 写 outbox(order.paid)。幂等：已 paid 直接返回成功。
	MarkPaid(ctx context.Context, fact PaidFact) error
	// Cancel 取消（用户/管理员/超时 TTL 任务）；paid 之后禁止回 canceled（§5.3）。
	Cancel(ctx context.Context, orderNo, reason string, operator Operator) error
}

// QueryResult 取货结果（取货三重门通过后返回，铁律 12）。
type QueryResult struct {
	OrderNo  string
	Status   string
	Total    money.Cents
	Items    []DeliveryItem
	FetchCnt int32 // 已取货次数（>1 走重新显示流程 + 审计）
}

// DeliveryItem 交付项（内容为一次性解密明文——仅此内存态，永不落库/日志）。
type DeliveryItem struct {
	ItemID      uint64
	ProductName string
	Content     string
	Masked      bool // true = 掩码展示（尾 4 位），需重新显示流程
}

// Operator 操作者（状态事件溯源 operator 字段）。
type Operator struct {
	Type string // system / user / admin / worker
	ID   uint64
	IP   string
}

// OrderQuery 取货查询窄接口（storefront 消费）。
type OrderQuery interface {
	// QueryByNo 单号 + 查询密码取货（constant-time 比对；密码错误与单号错误对外表现一致）。
	QueryByNo(ctx context.Context, orderNo, queryPassword, clientIP string) (*QueryResult, error)
}

// SoldCounter 批量已售数量（管理列表展示消费，通道 A）。
type SoldCounter interface {
	// SoldBatch 各商品已售数量（paid 及之后状态订单的 order_items.quantity 聚合）。
	SoldBatch(ctx context.Context, productIDs []uint64) (map[uint64]int64, error)
}
