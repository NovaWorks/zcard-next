package supply

// wire providers。

import "github.com/google/wire"

// ProviderSet supply providers。
var ProviderSet = wire.NewSet(NewSupplyService)
