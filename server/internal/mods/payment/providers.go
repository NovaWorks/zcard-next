package payment

// wire providers。

import "github.com/google/wire"

// ProviderSet payment providers。
var ProviderSet = wire.NewSet(
	NewPaymentRepoImpl,
	NewRegistry,
	NewPaymentUsecase,
	NewAdminPaymentService,
	NewStorePaymentService,
)
