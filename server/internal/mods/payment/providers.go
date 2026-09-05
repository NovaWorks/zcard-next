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
	// ：订单退款入口（procurement 消费，通道 A）
	wire.Bind(new(port.OrderRefunder), new(*PaymentRepoImpl)),
	// ：充值支付单创建端口（wallet 模块消费，通道 A）
	wire.Bind(new(port.RechargePayer), new(*PaymentRepoImpl)),
	// ：慢通道 pending 探测（order 超时取消顺延消费，通道 A）
	wire.Bind(new(port.SlowPaymentChecker), new(*PaymentRepoImpl)),
)
