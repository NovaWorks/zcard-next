package dashboard

// wire providers。

import "github.com/google/wire"

// ProviderSet dashboard providers。
var ProviderSet = wire.NewSet(NewDashboardRepoImpl, NewAdminDashboardService)
