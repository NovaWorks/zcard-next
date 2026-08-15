package order

import "testing"

// 状态机白名单（§5.3 必测项：非法迁移必须拒绝）。
func TestAllow(t *testing.T) {
	allowed := [][2]string{
		{StatusPendingPayment, StatusPaid},
		{StatusPendingPayment, StatusCanceled},
		{StatusPendingPayment, StatusExpired},
		{StatusPaid, StatusFulfilling},
		{StatusPaid, StatusManualPending},
		{StatusPaid, StatusRefundPending},
		{StatusFulfilling, StatusDelivered},
		{StatusFulfilling, StatusPartiallyDelivered},
		{StatusDelivered, StatusCompleted},
		{StatusRefundPending, StatusRefunded},
		{StatusRefundPending, StatusPaid}, // 退款被拒/撤销
	}
	for _, c := range allowed {
		if !Allow(c[0], c[1]) {
			t.Errorf("合法迁移被拒绝: %s → %s", c[0], c[1])
		}
	}
}

func TestAllowForbidden(t *testing.T) {
	forbidden := [][2]string{
		// paid 之后禁止回 canceled（退款走独立路径，铁律 §5.3）
		{StatusPaid, StatusCanceled},
		{StatusFulfilling, StatusCanceled},
		{StatusDelivered, StatusCanceled},
		// 终态不可迁出
		{StatusCompleted, StatusPaid},
		{StatusRefunded, StatusPaid},
		{StatusCanceled, StatusPaid},
		{StatusExpired, StatusPaid},
		// 未支付不可发货/完成
		{StatusPendingPayment, StatusFulfilling},
		{StatusPendingPayment, StatusDelivered},
		// 跳变非法
		{StatusPendingPayment, StatusRefunded},
		{StatusPaid, StatusCompleted},
	}
	for _, c := range forbidden {
		if Allow(c[0], c[1]) {
			t.Errorf("非法迁移被放行: %s → %s", c[0], c[1])
		}
	}
}

func TestCalcParentStatus(t *testing.T) {
	cases := []struct {
		items []string
		want  string
	}{
		{[]string{"delivered", "delivered"}, StatusDelivered},
		{[]string{"delivered", "pending"}, StatusPartiallyDelivered},
		{[]string{"manual_pending", "delivered"}, StatusManualPending},
		{[]string{"refunded", "delivered"}, StatusRefunded},
		{[]string{"pending"}, StatusPaid},
	}
	for _, c := range cases {
		if got := CalcParentStatus(c.items); got != c.want {
			t.Errorf("CalcParentStatus(%v) = %s, want %s", c.items, got, c.want)
		}
	}
}
