package authz

// wire providers（RbacUsecase 同时实现 port.Authorizer，跨模块消费经 port 绑定）。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/authz/port"

	"github.com/google/wire"
)

// ProviderSet authz providers。
var ProviderSet = wire.NewSet(
	NewRbacUsecase,
	NewRoleRepoImpl,
	wire.Bind(new(RoleRepo), new(*RoleRepoImpl)),
	wire.Bind(new(port.Authorizer), new(*RbacUsecase)),
)
