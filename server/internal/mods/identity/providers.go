package identity

// wire providers（模块自装配；跨模块绑定在 bootstrap）。

import "github.com/google/wire"

// ProviderSet identity providers。
var ProviderSet = wire.NewSet(
	NewIdentityUsecase,
	NewAdminUserRepoImpl,
	wire.Bind(new(AdminUserRepo), new(*AdminUserRepoImpl)),
	NewAdminAuthService,
)
