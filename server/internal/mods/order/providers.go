package order

// wire providers。

import "github.com/google/wire"

// ProviderSet order providers。
var ProviderSet = wire.NewSet(
	NewOrderUsecaseDep,
	NewOrderRepoImpl,
	NewStoreOrderService,
	NewAdminOrderService,
	// §4.6 破环点：OrderLifecycle 端口（payment 回调消费；适配器返回接口类型）
	ProvideOrderLifecycle,
)
