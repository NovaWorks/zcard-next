// Package order 订单模块（M1a）：下单用例、父子订单、状态机、价格计算管线、
// 查询密码、取货三重门。M0 落地状态机纯逻辑（白名单迁移 + 审计语义）。
package order

import "errors"

// 订单状态（§5.3 状态机；与 ent schema orders.status 枚举一一对应）。
const (
	StatusPendingPayment     = "pending_payment"
	StatusPaid               = "paid"
	StatusFulfilling         = "fulfilling"
	StatusPartiallyDelivered = "partially_delivered"
	StatusDelivered          = "delivered"
	StatusCompleted          = "completed"
	StatusCanceled           = "canceled"
	StatusExpired            = "expired"
	StatusRefundPending      = "refund_pending"
	StatusRefunded           = "refunded"
	StatusManualPending      = "manual_pending" // 全部人工发货（父状态聚合用）
)

// transitions 状态机白名单（Allow(from, to)）。paid 之后禁止回 canceled（退款走独立路径）。
var transitions = map[string]map[string]bool{
	StatusPendingPayment: {
		StatusPaid: true, StatusCanceled: true, StatusExpired: true,
	},
	StatusPaid: {
		StatusFulfilling: true, StatusManualPending: true, StatusRefundPending: true,
	},
	StatusFulfilling: {
		StatusPartiallyDelivered: true, StatusDelivered: true, StatusRefundPending: true,
	},
	StatusPartiallyDelivered: {
		StatusDelivered: true, StatusRefundPending: true,
	},
	StatusDelivered: {
		StatusCompleted: true, StatusRefundPending: true,
	},
	StatusManualPending: {
		StatusFulfilling: true, StatusDelivered: true, StatusRefundPending: true,
	},
	StatusRefundPending: {
		StatusRefunded: true, StatusPaid: true, // 退款被拒/撤销回 paid
	},
	StatusCompleted: {},
	StatusCanceled:  {},
	StatusExpired:   {},
	StatusRefunded:  {},
}

// ErrTransitionNotAllowed 非法状态迁移（状态机红灯，必测项 §5.3）。
var ErrTransitionNotAllowed = errors.New("order.TRANSITION_NOT_ALLOWED")

// Allow 判定状态迁移是否合法（每次迁移落 order_status_events，事件溯源）。
func Allow(from, to string) bool {
	allowed, ok := transitions[from]
	if !ok {
		return false
	}
	return allowed[to]
}

// CalcParentStatus 父状态由子项履约状态聚合（优先级：refunded > canceled >
// manual_pending > partially_delivered > fulfilling > delivered > completed > paid）。
// M1 交付完整实现（此处先固定优先级口径供 data 层引用）。
func CalcParentStatus(itemStatuses []string) string {
	// 优先级从高到低扫描，任一子项命中即取该状态
	priority := []string{StatusRefunded, StatusCanceled, StatusManualPending, StatusPartiallyDelivered, StatusFulfilling, StatusDelivered, StatusPaid}
	counts := map[string]int{}
	for _, s := range itemStatuses {
		counts[s]++
	}
	for _, p := range priority {
		if counts[p] > 0 {
			// 部分 vs 全部：子项存在 delivered 且存在未 delivered → partially_delivered
			if p == StatusDelivered && counts[StatusDelivered] < len(itemStatuses) {
				return StatusPartiallyDelivered
			}
			return p
		}
	}
	return StatusPaid
}

// OrderUsecase 订单用例骨架（M1a 交付下单管线/超时取消/取货三重门）。
type OrderUsecase struct{}

// NewOrderUsecase 构造。
func NewOrderUsecase() *OrderUsecase { return &OrderUsecase{} }
