package inventory

// wire providers（CardCipher 由 bootstrap 按密钥解析构造）。

import "github.com/google/wire"

// ProviderSet inventory providers。
var ProviderSet = wire.NewSet(
	NewInventoryUsecase,
	NewCardRepoImpl,
	wire.Bind(new(CardRepo), new(*CardRepoImpl)),
	NewAdminInventoryService,
)
