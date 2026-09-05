package procurement

// wire providers（）。
//
// 跨模块绑定（通道 A，装配集中在 bootstrap）：
// - svc.gw ← supplyport.UpstreamGateway（supply.Gateway）
// - svc.reader ← catalogport.ProductReader（catalog.ProductRepoImpl）
// - svc.attach ← fulfillmentport.AttachUpstreamDelivery（fulfillment.DeliveryRepoImpl）
// - svc.refund ← paymentport.OrderRefunder（payment.PaymentRepoImpl）
// - svc.outbox ← events.Writer（data.OutboxWriter）

import "github.com/google/wire"

// ProviderSet procurement providers。
var ProviderSet = wire.NewSet(
	NewProcureRepo,
	NewProcureService,
	NewAdminProcurementService,
)
