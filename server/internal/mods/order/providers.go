package order

// wire providers。

import "github.com/google/wire"

// ProviderSet order providers。
var ProviderSet = wire.NewSet(NewOrderUsecase, NewOrderRepoImpl)
