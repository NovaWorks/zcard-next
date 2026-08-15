package fulfillment

// wire providers。

import "github.com/google/wire"

// ProviderSet fulfillment providers。
var ProviderSet = wire.NewSet(NewFulfillmentUsecase, NewDeliveryRepoImpl)
