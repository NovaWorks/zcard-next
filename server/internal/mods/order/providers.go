package order

// wire providers。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/order/port"

	"github.com/google/wire"
)

// ProviderSet order providers。
var ProviderSet = wire.NewSet(
	NewOrderUsecaseDep,
	NewOrderRepoImpl,
	NewStoreOrderService,
	NewStoreCartService,
	NewAdminOrderService,
	// 破环点：OrderLifecycle 端口（payment 回调消费；适配器返回接口类型）
	ProvideOrderLifecycle,
	// 管理列表已售聚合（catalog 消费，通道 A）
	wire.Bind(new(port.SoldCounter), new(*OrderUsecase)),
)
