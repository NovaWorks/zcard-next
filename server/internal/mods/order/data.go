package order

// 订单仓储骨架（M1a 交付：创建订单 + 锁卡同事务、金额行快照、状态事件、
// 超时取消扫描、游标分页）。当前注册 wire provider。

import "github.com/NovaWorks/zcard-next/server/internal/data"

// OrderRepoImpl 订单仓储实现（M1a 交付方法体）。
type OrderRepoImpl struct {
	data *data.Data
}

// NewOrderRepoImpl 构造。
func NewOrderRepoImpl(d *data.Data) *OrderRepoImpl { return &OrderRepoImpl{data: d} }
