package settings

// wire providers。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"

	"github.com/google/wire"
)

// ProviderSet settings providers。
var ProviderSet = wire.NewSet(
	NewSettingsUsecase,
	NewRepoImpl,
	wire.Bind(new(Repo), new(*RepoImpl)),
	wire.Bind(new(port.Provider), new(*RepoImpl)),
	wire.Bind(new(port.CurrencyReader), new(*RepoImpl)),
	NewAdminSettingsService,
	NewStorefrontConfigService,
	NewAdminCurrencyService,
)
