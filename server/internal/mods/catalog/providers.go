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
	// P2-01：货源同步商品 upsert 端口绑定（supply 模块消费，通道 A）
	wire.Bind(new(port.UpstreamProductWriter), new(*ProductRepoImpl)),
	// P2-02：商品读取端口（procurement 消费，通道 A）
	wire.Bind(new(port.ProductReader), new(*ProductRepoImpl)),
	// P2-03：供货目录端口（supplier 消费，通道 A）
	wire.Bind(new(port.SupplierCatalog), new(*ProductRepoImpl)),
	NewStoreCatalogService,
	NewAdminCatalogService,
)
