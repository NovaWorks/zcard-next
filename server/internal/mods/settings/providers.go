package settings

// wire providers。

import "github.com/google/wire"

// ProviderSet settings providers。
var ProviderSet = wire.NewSet(
	NewSettingsUsecase,
	NewRepoImpl,
	wire.Bind(new(Repo), new(*RepoImpl)),
	NewAdminSettingsService,
)
