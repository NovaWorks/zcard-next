// Package events 事务性 Outbox 事件契约（规划 §4.8 / 附录 C）。
//
// 通道 C：业务事务内写 outbox_events（与业务同事务提交），relay（500ms 扫描）
// 投递 asynq 或进程内分发；dedupe_key 唯一索引防重复发布；
// 消费方经 processed_events(event_id, consumer) 幂等。
//
// M0 定义契约与事件目录；relay 与进程内分发器 M1 随交易闭环交付。
package events

import "encoding/json"

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
	ProcurementFulfilled = "procurement.fulfilled"
	ProcurementFailed    = "procurement.failed"
)

// Envelope outbox 事件信封（写入 outbox_events 的载荷结构）。
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

// Writer outbox 写入端口：必须与业务在同一事务内调用（通道 C 的关键约束）。
// 实现位于 internal/data（ent 收口）；dedupe_key 重复时返回已存在事件不重复发布。
type Writer interface {
	Write(module, typ, aggregateID, dedupeKey string, payload json.RawMessage) error
}
