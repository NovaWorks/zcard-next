package fulfillment

// wire providers。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment/port"

	"github.com/google/wire"
)

// ProviderSet fulfillment providers。
var ProviderSet = wire.NewSet(
	NewDeliveryRepoImpl,
	NewStoreDeliveryService,
	NewAdminFulfillmentService,
	// ：上游采购交付出口（procurement 消费，通道 A）
	wire.Bind(new(port.AttachUpstreamDelivery), new(*DeliveryRepoImpl)),
)
