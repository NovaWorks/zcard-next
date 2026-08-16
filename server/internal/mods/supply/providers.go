package supply

// wire providers（P2-01）。

import (
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
	NewGateway,
	wire.Bind(new(port.UpstreamGateway), new(*Gateway)),
	NewSupplyService, // 对外供货 Ping 骨架（P2-03 完整协议由 supplier 模块接管）
)
