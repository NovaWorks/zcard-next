package payment

// wire providers。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"

	"github.com/google/wire"
)

// ProviderSet payment providers。
var ProviderSet = wire.NewSet(
	NewPaymentRepoImpl,
	NewRegistry,
	NewPaymentUsecase,
	NewAdminPaymentService,
	NewStorePaymentService,
	// P2-02：订单退款入口（procurement 失败策略消费，通道 A）
	wire.Bind(new(port.OrderRefunder), new(*PaymentRepoImpl)),
)
