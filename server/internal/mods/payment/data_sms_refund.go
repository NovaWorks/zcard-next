package payment

import (
	"context"
	"encoding/json"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	si "github.com/NovaWorks/zcard-next/server/internal/data/ent/smsintent"
)

// RefundSMS reuses the order's native refund model. The stable intent is locked
// before computing the remaining retail amount, and commits with the wallet and outbox.
func (r *PaymentRepoImpl) RefundSMS(ctx context.Context, id uint64) (refundID uint64, err error) {
	err = data.Tx(ctx, r.data, func(ctx context.Context) error {
		c := data.Client(ctx, r.data)
		if e := c.SMSIntent.UpdateOneID(id).AddAttempts(0).Exec(ctx); e != nil {
			return e
		}
		row, e := c.SMSIntent.Get(ctx, id)
		if e != nil {
			return e
		}
		if row.RetailRefundState == "succeeded" {
			refundID = row.RefundID
			return nil
		}
		if !data.SMSRefundConfirmed(row) {
			return refundInvalid("接码供货退款凭证尚未确认")
		}
		o, e := c.Order.Get(ctx, row.OrderID)
		if e != nil {
			return e
		}
		items, e := c.OrderItem.Query().Where(orderitem.OrderID(o.ID)).All(ctx)
		if e != nil {
			return e
		}
		if len(items) != 1 || items[0].ID != row.OrderItemID || items[0].DeliveryKind != "sms_activation" || o.UserID != row.UserID || o.SubsiteID != row.SubsiteID || o.BaseCurrency != "CNY" || o.TotalAmount <= 0 {
			return refundInvalid("接码零售订单身份或金额异常")
		}
		receipts, e := c.RefundOrder.Query().Where(refundorder.OrderID(o.ID)).All(ctx)
		if e != nil {
			return e
		}
		var refunded int64
		for _, rf := range receipts {
			if rf.Status == refundorder.StatusCreated || rf.Status == refundorder.StatusProcessing {
				return refundInvalid("已有退款待核对")
			}
			if rf.Status == refundorder.StatusSucceeded {
				if rf.Amount < 0 || rf.Amount > o.TotalAmount-refunded {
					return refundInvalid("历史退款金额异常")
				}
				refunded += rf.Amount
			}
		}
		remaining := o.TotalAmount - refunded
		reference := fmt.Sprintf("sms_retail_refund:%d", row.OrderItemID)
		var result *ent.RefundOrder
		if remaining > 0 {
			if o.CommerceVersion == 1 {
				it := items[0]
				if it.PaidAmount-it.RefundedAmount != remaining {
					return refundInvalid("商品项退款分摊异常")
				}
				qty := int64(0)
				if it.FulfillmentStatus == "pending" {
					qty = int64(it.Quantity - it.CanceledQuantity)
				}
				raw, _ := json.Marshal([]map[string]int64{{"item_id": int64(it.ID), "amount_cents": remaining, "shipping_cents": 0, "cancel_quantity": qty}})
				result, e = r.RefundPhysical(ctx, o.ID, 0, &adminv1.CreateRefundRequest{AmountCents: remaining, Channel: "wallet", Reason: "接码供货已确认退款", RequestKey: reference, ExpectedRefundedCents: &refunded, ItemAllocationsJson: string(raw)})
			} else if o.CommerceVersion == 0 {
				result, e = r.RefundToWallet(ctx, o.ID, remaining, &refunded, "接码供货已确认退款", 0)
			} else {
				return refundInvalid("未知订单账务版本")
			}
			if e != nil {
				return e
			}
			refundID = result.ID
			if e = c.RefundOrder.UpdateOneID(result.ID).SetRequestKey(reference).Exec(ctx); e != nil {
				return e
			}
		}
		return c.SMSIntent.Update().Where(si.ID(id)).SetRefundID(refundID).SetRetailRefundState("succeeded").SetPhase("done").SetLeaseUntil(0).SetLastError("").Exec(ctx)
	})
	return
}
