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
	// P3-01 M3：等级进度 storefront 面 + 积分产生事件订阅
	NewStoreMemberLevelService,
	NewPointsService,
)
