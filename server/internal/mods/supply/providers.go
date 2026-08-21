package supply

// wire providers（P2-01）。

import (
	dashboardport "github.com/NovaWorks/zcard-next/server/internal/mods/dashboard/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"

	"github.com/google/wire"
)

// ProviderSet supply providers。
// 跨模块绑定（通道 A，装配集中在 bootstrap）：
//   - SyncService.writer ← catalogport.UpstreamProductWriter（catalog.ProductRepoImpl）
//   - SyncService.outbox ← events.Writer（data.OutboxWriter）
//   - Gateway ← supplyport.UpstreamGateway（P2-02 procurement 消费）
var ProviderSet = wire.NewSet(
	NewSupplyRepoImpl,
	NewSyncService,
	NewAdminSupplyService,
	NewPacer,
	NewScheduler,
	NewGateway,
	wire.Bind(new(port.UpstreamGateway), new(*Gateway)),
	// P3-07：对账上游数据源端口（dashboard 消费，通道 A）
	wire.Bind(new(dashboardport.UpstreamOrderSource), new(*Gateway)),
)
