package memberlevel

// wire providers。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel/port"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	pluginport "github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/google/wire"
)

// ProviderSet memberlevel providers。
var ProviderSet = wire.NewSet(
	ProvideProtectedMemberLevelRepo,
	wire.Bind(new(port.RateResolver), new(*MemberLevelRepoImpl)),
	NewAdminMemberLevelService,
	// ：等级进度 storefront 面 + 积分产生事件订阅
	NewStoreMemberLevelService,
	NewPointsService,
)

// ProvideProtectedMemberLevelRepo requires the global plugin reference guard in
// production; isolated legacy unit tests may still construct the plain repo.
func ProvideProtectedMemberLevelRepo(d *data.Data, recharge walletport.RechargeReader, rules pluginport.RequirementStore) *MemberLevelRepoImpl {
	r := NewMemberLevelRepoImpl(d, recharge)
	r.pluginRules = rules
	return r
}
