package update

// wire providers。

import (
	"github.com/google/wire"
)

// ProviderSet update providers。
var ProviderSet = wire.NewSet(NewService, NewAdminUpdateService)
