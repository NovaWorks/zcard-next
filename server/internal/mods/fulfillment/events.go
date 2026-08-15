package fulfillment

// fulfillment 模块事件声明（附录 C）。
//
// 发布：
//   - order.delivered  全部交付完成（触发通知/完成态流转）
//
// 订阅：
//   - payment.succeeded → 本地卡密项履约（upstream 项由 procurement 接管，M2）
//   - order.paid        → 履约编排入口

import "github.com/NovaWorks/zcard-next/server/internal/platform/events"

const EventDelivered = events.OrderDelivered
