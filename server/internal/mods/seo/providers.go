package seo

// wire providers。

import "github.com/google/wire"

// ProviderSet seo providers。
var ProviderSet = wire.NewSet(
	NewSeoRepo,
	NewSeoService,
)
