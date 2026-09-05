package content

// wire providers（）。

import "github.com/google/wire"

// ProviderSet content providers。
var ProviderSet = wire.NewSet(
	NewContentRepo,
	NewAdminContentService,
	NewStoreContentService,
)
