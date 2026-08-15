package inventory

// 卡密仓储骨架（M1 随下单链路交付完整实现：FOR UPDATE 锁卡 / SQLite CAS affected
// rows / TTL 释放扫描 / 导入批次撤销）。未实现路径显式返回 ErrNotImplemented，
// 绝不静默返回零值。

import (
	"context"
	"errors"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
)

// ErrNotImplemented M1 交付路径占位（调用方按未上线能力处理，非内部错误）。
var ErrNotImplemented = errors.New("inventory.NOT_IMPLEMENTED（M1 交付）")

// CardRepoImpl 卡密仓储实现。
type CardRepoImpl struct {
	data *data.Data
}

// NewCardRepoImpl 构造。
func NewCardRepoImpl(d *data.Data) *CardRepoImpl { return &CardRepoImpl{data: d} }

// Reserve 事务内锁卡（M1：FOR UPDATE 锁可用行 → reserved；SQLite 走 CAS）。
func (*CardRepoImpl) Reserve(context.Context, uint64, []port.ReserveItem) (*port.Reservation, error) {
	return nil, ErrNotImplemented
}

// Release 释放预留（M1：订单取消/超时 + TTL 周期任务兜底）。
func (*CardRepoImpl) Release(context.Context, string) error { return ErrNotImplemented }

// MarkUsed 售出标记（M1：校验 affected rows 防并发重发）。
func (*CardRepoImpl) MarkUsed(context.Context, []uint64, uint64) error { return ErrNotImplemented }

// Stock 可用库存数（M1：覆盖索引 COUNT；链接类返回 -1）。
func (*CardRepoImpl) Stock(context.Context, uint64, uint64) (int64, error) {
	return 0, ErrNotImplemented
}

var _ CardRepo = (*CardRepoImpl)(nil)
