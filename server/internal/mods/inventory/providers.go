package inventory

// wire providers（CardCipher 由 bootstrap 按密钥解析构造）。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"

	"github.com/google/wire"
)

// ProviderSet inventory providers。
var ProviderSet = wire.NewSet(
	NewInventoryUsecase,
	NewCardRepoImpl,
	wire.Bind(new(CardRepo), new(*CardRepoImpl)),
	wire.Bind(new(port.Inventory), new(*CardRepoImpl)),
	NewAdminInventoryService,
)
