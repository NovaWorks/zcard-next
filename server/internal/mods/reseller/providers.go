package reseller

// wire providers（）。

import (
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/reseller/port"

	"github.com/google/wire"
)

// ProviderSet reseller providers。
var ProviderSet = wire.NewSet(
	NewResellerRepo,
	NewAdminResellerService,
	NewSettleService,
	// 管线步骤 7 端口绑定（order 模块消费，通道 A）
	wire.Bind(new(port.Pricer), new(*ResellerRepo)),
	// 白标品牌解析端口绑定（notify 消费，通道 A——fail-closed 分站品牌）
	wire.Bind(new(notifyport.BrandResolver), new(*ResellerRepo)),
)
