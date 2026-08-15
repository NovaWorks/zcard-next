package fulfillment

// 交付记录仓储骨架（M1 交付：交付记录写入、一次性令牌、取货计数与掩码、取货审计）。

import "github.com/NovaWorks/zcard-next/server/internal/data"

// DeliveryRepoImpl 交付仓储实现（M1 交付方法体）。
type DeliveryRepoImpl struct {
	data *data.Data
}

// NewDeliveryRepoImpl 构造。
func NewDeliveryRepoImpl(d *data.Data) *DeliveryRepoImpl { return &DeliveryRepoImpl{data: d} }
