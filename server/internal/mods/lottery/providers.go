package lottery

import "github.com/google/wire"

var ProviderSet = wire.NewSet(NewRepo, NewAdminService, NewStoreService)
