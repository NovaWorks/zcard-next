package settings

// wire providers。

import (
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"

	"github.com/google/wire"
)

// ProviderSet settings providers。
var ProviderSet = wire.NewSet(
	NewSettingsUsecase,
	ProvideRepo,
	wire.Bind(new(Repo), new(*RepoImpl)),
	wire.Bind(new(port.Provider), new(*RepoImpl)),
	wire.Bind(new(port.CurrencyReader), new(*RepoImpl)),
	NewAdminSettingsService,
	NewAdminInstallService,
	NewStorefrontConfigService,
	NewAdminCurrencyService,
)

func ProvideRepo(d *data.Data, box *crypto.Box) *RepoImpl {
	r := NewRepoImpl(d)
	r.cipher = box
	return r
}
