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
	// P2-03：供货交付卡密读取（supplier 消费，通道 A）
	wire.Bind(new(port.CardContentReader), new(*CardRepoImpl)),
	NewAdminInventoryService,
)
