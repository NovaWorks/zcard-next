package supply

// wire providers（P2-01）。

import "github.com/google/wire"

// ProviderSet supply providers。
// 跨模块绑定（通道 A，装配集中在 bootstrap）：
//   - SyncService.writer ← catalogport.UpstreamProductWriter（catalog.ProductRepoImpl）
//   - SyncService.outbox ← events.Writer（data.OutboxWriter）
var ProviderSet = wire.NewSet(
	NewSupplyRepoImpl,
	NewSyncService,
	NewAdminSupplyService,
	NewSupplyService, // 对外供货 Ping 骨架（P2-03 完整协议由 supplier 模块接管）
)
