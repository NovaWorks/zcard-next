package catalog

// wire providers。

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"

	"github.com/google/wire"
)

// settingsReaderAdapter settings.RepoImpl → port.SettingsReader 适配
// （低库存阈值读取；读失败走默认值，不阻断商品列表）。
type settingsReaderAdapter struct{ repo *settings.RepoImpl }

func (a settingsReaderAdapter) GetJSON(ctx context.Context, group, key string) ([]byte, error) {
	raw, err := a.repo.GetDefault(ctx, group, key, nil)
	if err != nil {
		return nil, nil
	}
	return raw, nil
}

// ProvideSettingsReader settings 适配为通用端口（通道 A）。
func ProvideSettingsReader(repo *settings.RepoImpl) port.SettingsReader {
	return settingsReaderAdapter{repo: repo}
}

// ProviderSet catalog providers。
var ProviderSet = wire.NewSet(
	NewCatalogUsecase,
	NewProductRepoImpl,
	ProvideSettingsReader, // port.SettingsReader 绑定（低库存阈值等）
	wire.Bind(new(ProductRepo), new(*ProductRepoImpl)),
	wire.Bind(new(port.PricingResolver), new(*ProductRepoImpl)),
	// ：货源同步商品 upsert 端口绑定（supply 模块消费，通道 A）
	wire.Bind(new(port.UpstreamProductWriter), new(*ProductRepoImpl)),
	// S1：货源轻量维护端口（price/status scope + 删除对账）
	wire.Bind(new(port.UpstreamProductMaintainer), new(*ProductRepoImpl)),
	// ：商品读取端口（procurement 消费，通道 A）
	wire.Bind(new(port.ProductReader), new(*ProductRepoImpl)),
	// ：供货目录端口（supplier 消费，通道 A）
	wire.Bind(new(port.SupplierCatalog), new(*ProductRepoImpl)),
	NewStoreCatalogService,
	NewAdminCatalogService,
)
