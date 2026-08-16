package affiliate

// wire providers（P3-03）。
//
// 跨模块绑定（通道 A）：
//   - engine.wallet ← walletport.Wallet（冻结确认入账/逆向扣回）
//   - engine.settings ← notifyport.SettingsReader（affiliate.affiliate 配置组）
//   - CommissionRepo 实现 port.CommissionReader（dashboard 统计）

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/affiliate/port"

	"github.com/google/wire"
)

// ProviderSet affiliate providers。
var ProviderSet = wire.NewSet(
	NewCommissionRepo,
	NewAffiliateService,
	NewStoreAffiliateService,
	wire.Bind(new(port.CommissionReader), new(*CommissionRepo)),
)
