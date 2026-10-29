package payment

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/socialoperation"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/socialtask"
	"time"
)

// RefundSocial credits only the increase in confirmed cumulative entitlement.
// Native order refunds, wallet entries and the task checkpoint commit atomically.
func (r *PaymentRepoImpl) RefundSocial(ctx context.Context, taskID string) (refundID uint64, err error) {
	err = data.Tx(ctx, r.data, func(ctx context.Context) error {
		c := data.Client(ctx, r.data)
		if e := c.SocialTask.UpdateOneID(taskID).AddVersion(0).Exec(ctx); e != nil {
			return e
		}
		row, e := c.SocialTask.Get(ctx, taskID)
		if e != nil {
			return e
		}
		if row.Role != "retail" {
			return refundInvalid("非零售服务退款")
		}
		o, e := c.Order.Get(ctx, row.OrderID)
		if e != nil {
			return e
		}
		items, e := c.OrderItem.Query().Where(orderitem.OrderID(o.ID)).All(ctx)
		if e != nil {
			return e
		}
		if len(items) != 1 || items[0].ID != row.OrderItemID || items[0].DeliveryKind != "social_service" || o.UserID != row.UserID || o.SubsiteID != row.SubsiteID || o.BaseCurrency != "CNY" || o.CommerceVersion != 0 || o.TotalAmount != row.RetailPaidAmount {
			return refundInvalid("服务零售订单身份或金额异常")
		}
		paid, e := c.Payment.Query().Where(payment.OrderID(o.ID), payment.StatusEQ(payment.StatusSuccess), payment.ReviewReason("")).Only(ctx)
		if e != nil || paid.Amount != o.TotalAmount || paid.Fee != 0 {
			return refundInvalid("服务实付款凭证异常")
		}
		target, has, e := data.SocialRetailRefundLimit(ctx, c, o.ID)
		if e != nil {
			return e
		}
		if !has {
			return refundInvalid("服务订单凭证缺失")
		}
		rows, e := c.RefundOrder.Query().Where(refundorder.OrderID(o.ID)).All(ctx)
		if e != nil {
			return e
		}
		var refunded int64
		for _, rf := range rows {
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
		if refunded > target {
			return refundInvalid("退款超过已确认服务额度，请核对")
		}
		refundID = row.RefundID
		if target > refunded {
			receipt, e := r.RefundToWallet(ctx, o.ID, target-refunded, &refunded, "服务供货已确认退款", 0)
			if e != nil {
				return e
			}
			refundID = receipt.ID
			key := fmt.Sprintf("%x", sha256.Sum256([]byte("social_refund:"+row.ID+":"+row.RefundReference)))
			if e = c.RefundOrder.UpdateOneID(receipt.ID).SetRequestKey(key).SetUpstreamRefundID(fmt.Sprintf("%x", sha256.Sum256([]byte(row.RefundReference)))).Exec(ctx); e != nil {
				return e
			}
		}
		phase := "query"
		status := "partially_refunded"
		if target == o.TotalAmount {
			status = "succeeded"
			pending, e := c.SocialOperation.Query().Where(socialoperation.TaskID(row.ID), socialoperation.StatusIn("pending", "unknown")).Exist(ctx)
			if e != nil {
				return e
			}
			if !pending {
				phase = "done"
			}
		}
		if e = c.OrderItem.UpdateOneID(row.OrderItemID).SetRefundedAmount(target).Exec(ctx); e != nil {
			return e
		}
		return c.SocialTask.Update().Where(socialtask.ID(row.ID), socialtask.Role("retail")).SetRetailRefundedAmount(target).SetRetailRefundState(status).SetRefundID(refundID).SetPhase(phase).SetLeaseUntil(0).SetAttempts(0).SetLastError("").SetNextRunAt(time.Now().Add(10 * time.Second).Unix()).Exec(ctx)
	})
	return
}
