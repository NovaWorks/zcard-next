package license

// wire providers（P3-08）。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"

	"github.com/google/wire"
)

// ProviderSet license providers。
var ProviderSet = wire.NewSet(
	ProvideLicenseRepo,
	NewAdminLicenseService,
)

// ProvideLicenseRepo 装配（设置读写经 *settings.RepoImpl——通道 A，wire 注入）。
func ProvideLicenseRepo(repo *settings.RepoImpl) *LicenseRepo {
	return NewLicenseRepo(repo)
}
