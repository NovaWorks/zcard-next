package supplier

// wire providers（P2-03）。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	paymentport "github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"

	"github.com/google/wire"
)

// ProviderSet supplier providers。
var ProviderSet = wire.NewSet(
	NewSupplierRepoImpl,
	NewSupplyAPIService,
	NewAdminSupplierService,
	NewStoreSupplierService,
	// target=supply 充值入账端口（payment 回调消费；SupplierRepoImpl.Recharge 实现）
	wire.Bind(new(paymentport.SupplierRecharger), new(*SupplierRepoImpl)),
	// 依赖经 wire 结构匹配：SupplierCatalog ← catalog（已 Bind）、
	// Inventory/CardContentReader ← inventory、Enqueuer ← bootstrap
)

var _ = port.SupplierCatalog(nil)
