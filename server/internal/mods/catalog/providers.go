package catalog

// wire providers。

import "github.com/google/wire"

// ProviderSet catalog providers。
var ProviderSet = wire.NewSet(
	NewCatalogUsecase,
	NewProductRepoImpl,
	wire.Bind(new(ProductRepo), new(*ProductRepoImpl)),
	NewStoreCatalogService,
	NewAdminCatalogService,
)
