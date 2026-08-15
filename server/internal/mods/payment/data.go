package payment

// 支付单仓储骨架（M1a 交付：支付单生命周期、回调原文审计、补单、退款单状态机）。

import "github.com/NovaWorks/zcard-next/server/internal/data"

// PaymentRepoImpl 支付仓储实现（M1a 交付方法体）。
type PaymentRepoImpl struct {
	data *data.Data
}

// NewPaymentRepoImpl 构造。
func NewPaymentRepoImpl(d *data.Data) *PaymentRepoImpl { return &PaymentRepoImpl{data: d} }
