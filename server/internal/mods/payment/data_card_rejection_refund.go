package payment

import (
	"context"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
)

// RefundRejectedCard is deliberately narrower than the generic gateway refund
// entry point. A successful wallet payment and an authoritative zero-acceptance
// stock rejection are required; remote/unknown/delivered purchases stay manual.
func (r *PaymentRepoImpl) RefundRejectedCard(ctx context.Context, procurementID uint64) error {
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		c := data.Client(ctx, r.data)
		if e := c.ProcurementOrder.UpdateOneID(procurementID).AddRetryCount(0).Exec(ctx); e != nil {
			return e
		}
		po, e := c.ProcurementOrder.Get(ctx, procurementID)
		if e != nil {
			return e
		}
		key := fmt.Sprintf("card-rejection:%d", po.ID)
		if po.Status == "refunded" {
			receipt, e := c.RefundOrder.Query().Where(refundorder.RequestKey(key), refundorder.StatusEQ(refundorder.StatusSucceeded)).Only(ctx)
			if e != nil || receipt.Amount <= 0 {
				return refundInvalid("采购退款凭证缺失")
			}
			return nil
		}
		if po.Status != "refunding" || po.FailStrategy != "auto_refund" || po.UpstreamOrderID != "" || po.LastError != supplyport.ErrUpstreamNoStock.Error() {
			return refundInvalid("采购结果尚未证明可自动退款")
		}
		conn, e := c.SupplyConnection.Get(ctx, po.ConnectionID)
		if e != nil || conn.Driver != "zcard" {
			return refundInvalid("该采购需人工核对退款")
		}
		parts, e := c.ProcurementItem.Query().Where(procurementitem.ProcurementID(po.ID)).All(ctx)
		if e != nil {
			return e
		}
		if len(parts) != 1 {
			return refundInvalid("采购明细不完整")
		}
		for _, part := range parts {
			if len(part.ReceivedContent) > 0 {
				return refundInvalid("采购已有交付回执")
			}
		}
		it, e := c.OrderItem.Get(ctx, po.OrderItemID)
		if e != nil {
			return e
		}
		if it.Quantity <= 0 || parts[0].Quantity != it.Quantity {
			return refundInvalid("采购数量与订单不符")
		}
		o, e := c.Order.Get(ctx, it.OrderID)
		if e != nil {
			return e
		}
		items, e := c.OrderItem.Query().Where(orderitem.OrderID(o.ID)).All(ctx)
		if e != nil {
			return e
		}
		if len(items) != 1 || items[0].ID != it.ID || it.FulfillmentType != "upstream" || it.DeliveryKind != "card" || it.FulfillmentStatus == "delivered" || it.FulfillmentStatus == "refunded" || it.CanceledQuantity != 0 || o.UserID == 0 || o.BaseCurrency != "CNY" || o.CommerceVersion != 0 || o.TotalAmount <= 0 || o.Status != order.StatusPaid && o.Status != order.StatusFulfilling {
			return refundInvalid("该订单需人工按商品项核对退款")
		}
		paid, e := c.Payment.Query().Where(payment.OrderID(o.ID), payment.StatusEQ(payment.StatusSuccess), payment.ReviewReason("")).Only(ctx)
		if e != nil || paid.DriverSnapshot != "wallet" || paid.Amount != o.TotalAmount || paid.Fee != 0 {
			return refundInvalid("需要原始余额付款成功凭证")
		}
		receipts, e := c.RefundOrder.Query().Where(refundorder.OrderID(o.ID), refundorder.StatusNEQ(refundorder.StatusFailed)).Exist(ctx)
		if e != nil {
			return e
		}
		if receipts {
			return refundInvalid("已有其他退款，需人工核对")
		}
		refunded := int64(0)
		receipt, e := r.RefundToWallet(ctx, o.ID, o.TotalAmount, &refunded, "上游明确缺货且未受理采购，退回原付款余额", 0)
		if e != nil {
			return e
		}
		if e = c.RefundOrder.UpdateOneID(receipt.ID).SetRequestKey(key).Exec(ctx); e != nil {
			return e
		}
		if e = c.OrderItem.UpdateOneID(it.ID).SetRefundedAmount(o.TotalAmount).SetCanceledQuantity(it.Quantity).SetFulfillmentStatus("refunded").Exec(ctx); e != nil {
			return e
		}
		return c.ProcurementOrder.UpdateOneID(po.ID).SetStatus("refunded").SetUpstreamRefundID(fmt.Sprintf("wallet:%d", receipt.ID)).Exec(ctx)
	})
}
