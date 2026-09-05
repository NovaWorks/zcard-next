package wallet

// 钱包与账务模块（）：Reference 幂等键构造器（InTx 内核见 data_intx.go）。

import "fmt"

// Reference 幂等键构造（全账务统一规范，数据库架构 ）。
func Reference(kind string, ids ...uint64) string {
	s := kind
	for _, id := range ids {
		s = fmt.Sprintf("%s:%d", s, id)
	}
	return s
}

// 常用幂等键前缀。
const (
	RefOrderPay    = "order_pay"    // order_pay:<orderID>
	RefOrderRefund = "order_refund" // order_refund:<id>:<seq>
	RefRecharge    = "recharge"     // recharge:<payID>
	RefCommission  = "commission"   // commission:<id>
	RefAdjust      = "adjust"       // adjust:<auditID>
)
