package catalog

// wire providers。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"

	"github.com/google/wire"
)

// ProviderSet catalog providers。
var ProviderSet = wire.NewSet(
	NewCatalogUsecase,
	NewProductRepoImpl,
	wire.Bind(new(ProductRepo), new(*ProductRepoImpl)),
	wire.Bind(new(port.PricingResolver), new(*ProductRepoImpl)),
	NewStoreCatalogService,
	NewAdminCatalogService,
)
