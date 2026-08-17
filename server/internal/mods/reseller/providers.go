package reseller

// wire providers（P3-04）。

import (
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
)
