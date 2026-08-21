// Package events 事务性 Outbox 事件契约（规划 §4.8 / 附录 C）。
//
// 通道 C：业务事务内写 outbox_events（与业务同事务提交），relay（500ms 扫描）
// 投递 asynq 或进程内分发；dedupe_key 唯一索引防重复发布；
// 消费方经 processed_events(event_id, consumer) 幂等。
package events

import (
	"context"
	"encoding/json"
)

// 事件目录 v1（附录 C，M1 冻结；向后兼容规则：只加字段不改语义）。
const (
	OrderCreated         = "order.created"
	OrderPaid            = "order.paid"
	OrderDelivered       = "order.delivered"
	OrderCompleted       = "order.completed"
	OrderCanceled        = "order.canceled"
	OrderRefunded        = "order.refunded"
	PaymentSucceeded     = "payment.succeeded"
	PaymentFailed        = "payment.failed"
	RefundRequested      = "refund.requested"
	RefundSucceeded      = "refund.succeeded"
	WithdrawalReviewed   = "withdrawal.reviewed"
	TicketCreated        = "ticket.created"
	TicketReplied        = "ticket.replied"
	UserRegistered       = "user.registered"
	RechargeSucceeded    = "recharge.succeeded"
	SyncCompleted        = "sync.completed"
	SupplyRateLimited    = "supply.rate_limited"
	ProcurementFulfilled = "procurement.fulfilled"
	ProcurementFailed    = "procurement.failed"
)

// Envelope outbox 事件信封（队列任务载荷结构；task type = "event:"+Type）。
type Envelope struct {
	// EventID outbox_events.id（消费幂等锚点）
	EventID uint64 `json:"event_id"`
	// Type 事件类型（如 order.paid）
	Type string `json:"type"`
	// AggregateID 聚合根标识（订单号/支付单号）
	AggregateID string `json:"aggregate_id"`
	// SubsiteID 租户（worker 消费侧恢复租户上下文用）
	SubsiteID uint64 `json:"subsite_id"`
	// Payload 事件载荷（各模块 proto 定义）
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Writer outbox 写入端口：**必须在业务事务内调用**（通道 C 的关键约束）——
// 实现位于 internal/data（经 data.Tx 携带的 *ent.Tx 落库，事务回滚事件不残留）。
// dedupe_key 重复时返回 nil（幂等：视为已发布，不报错）。
type Writer interface {
	Write(ctx context.Context, module, typ, aggregateID, dedupeKey string, payload json.RawMessage) error
}

// Handler 事件处理器（消费方注册；实现必须幂等——processed_events 已兜底，
// 但处理器自身不应假设恰好一次）。
type Handler func(ctx context.Context, env Envelope) error

// All 事件目录全集（worker mux 注册与 CI 目录校验用；新增事件必须同步此处）。
func All() []string {
	return []string{
		OrderCreated, OrderPaid, OrderDelivered, OrderCompleted, OrderCanceled, OrderRefunded,
		PaymentSucceeded, PaymentFailed, RefundRequested, RefundSucceeded,
		WithdrawalReviewed, TicketCreated, TicketReplied,
		UserRegistered, RechargeSucceeded, SyncCompleted, SupplyRateLimited,
		ProcurementFulfilled, ProcurementFailed,
	}
}
