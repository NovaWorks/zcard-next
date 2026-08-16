package content

// wire providers（P2-04）。

import "github.com/google/wire"

// ProviderSet content providers。
var ProviderSet = wire.NewSet(
	NewContentRepo,
	NewAdminContentService,
	NewStoreContentService,
)
