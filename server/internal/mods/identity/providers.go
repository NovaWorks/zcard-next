package identity

// wire providers（模块自装配；跨模块绑定在 bootstrap）。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity/port"

	"github.com/google/wire"
)

// ProviderSet identity providers。
var ProviderSet = wire.NewSet(
	NewIdentityUsecase,
	NewAdminUserRepoImpl,
	wire.Bind(new(AdminUserRepo), new(*AdminUserRepoImpl)),
	wire.Bind(new(port.AdminMutator), new(*AdminUserRepoImpl)),
	wire.Bind(new(port.AdminReader), new(*AdminUserRepoImpl)),
	NewUserRepo,
	NewAdminAuthService,
	NewPasswordService,
	NewStoreUserService,
)
