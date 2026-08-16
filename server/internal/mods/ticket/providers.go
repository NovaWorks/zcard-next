package ticket

// wire providers（P3-05）。

import "github.com/google/wire"

// ProviderSet ticket providers。
var ProviderSet = wire.NewSet(
	NewTicketRepo,
	NewStoreTicketService,
	NewAdminTicketService,
)
