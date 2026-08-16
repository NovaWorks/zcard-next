package reseller

// wire providers（P3-04）。

import "github.com/google/wire"

// ProviderSet reseller providers。
var ProviderSet = wire.NewSet(
	NewResellerRepo,
	NewAdminResellerService,
)
