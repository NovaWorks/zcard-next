package order

// order 模块事件声明（附录 C 事件目录；经 outbox 发布，消费方幂等）。
//
// 发布：
//   - order.created    创建成功（锁卡完成）后
//   - order.paid       支付成功（payment 回调事务内或余额支付直发）
//   - order.delivered  全部交付
//   - order.completed  买家确认/查询完成
//   - order.canceled   取消（用户/管理员/超时）
//   - order.refunded   退款成功
//
// 订阅（M1）：payment.succeeded → MarkPaid + 触发履约。
// payload schema 由 proto 定义（M1 冻结；只加字段不改语义）。

import "github.com/NovaWorks/zcard-next/server/internal/platform/events"

// 本模块发布的事件类型（编译期绑定 platform/events 目录，防拼写漂移）。
const (
	EventCreated   = events.OrderCreated
	EventPaid      = events.OrderPaid
	EventDelivered = events.OrderDelivered
	EventCompleted = events.OrderCompleted
	EventCanceled  = events.OrderCanceled
	EventRefunded  = events.OrderRefunded
)

// DedupeKey outbox dedupe_key 构造（唯一索引防重复发布，§4.8）。
func DedupeKey(orderNo, action string) string { return "order:" + orderNo + ":" + action }
