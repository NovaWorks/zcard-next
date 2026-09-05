package license

// wire providers（）。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"

	"github.com/google/wire"
)

// ProviderSet license providers。
var ProviderSet = wire.NewSet(
	ProvideLicenseRepo,
	NewAdminLicenseService,
	// ：专业套餐在线购买（storefront 面）
	NewPurchaseRepo,
	NewStoreLicenseService,
)

// ProvideLicenseRepo 装配（设置读写经 *settings.RepoImpl——通道 A，wire 注入）。
func ProvideLicenseRepo(repo *settings.RepoImpl) *LicenseRepo {
	return NewLicenseRepo(repo)
}
