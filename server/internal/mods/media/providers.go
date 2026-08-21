package media

// wire providers（P3-06）。
//
// 跨模块绑定（通道 A）：
//   - MediaRepo 实现 mediaport.Uploader（ticket 附件/管理上传共用安全路径）
//   - MediaRepo 实现 mediaport.Referencer（catalog/content/ticket 引用计数）
// 引用注入方向：消费方在自己的 providers 里以依赖参数收 port.Referencer
// （media 不 import 消费方——无环）。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/media/port"

	"github.com/google/wire"
)

// ProviderSet media providers。
var ProviderSet = wire.NewSet(
	NewMediaRepo,
	NewAdminMediaService,
	NewStoreMediaService,
	wire.Bind(new(port.Uploader), new(*MediaRepo)),
	wire.Bind(new(port.Referencer), new(*MediaRepo)),
)
