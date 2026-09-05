package ticket

// wire providers（）。

import "github.com/google/wire"

// ProviderSet ticket providers。
var ProviderSet = wire.NewSet(
	NewTicketRepo,
	NewStoreTicketService,
	NewAdminTicketService,
)
