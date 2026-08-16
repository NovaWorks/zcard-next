package memberlevel

// wire providers。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel/port"

	"github.com/google/wire"
)

// ProviderSet memberlevel providers。
var ProviderSet = wire.NewSet(
	NewMemberLevelRepoImpl,
	wire.Bind(new(port.RateResolver), new(*MemberLevelRepoImpl)),
	NewAdminMemberLevelService,
)
