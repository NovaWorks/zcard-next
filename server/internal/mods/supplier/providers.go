package supplier

// wire providers（P2-03）。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"

	"github.com/google/wire"
)

// ProviderSet supplier providers。
var ProviderSet = wire.NewSet(
	NewSupplierRepoImpl,
	NewSupplyAPIService,
	NewAdminSupplierService,
	NewStoreSupplierService,
	// 依赖经 wire 结构匹配：SupplierCatalog ← catalog（已 Bind）、
	// Inventory/CardContentReader ← inventory、Enqueuer ← bootstrap
)

var _ = port.SupplierCatalog(nil)
