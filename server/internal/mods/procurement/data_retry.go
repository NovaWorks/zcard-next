package procurement

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
)

const submitRetryPrefix = "[采购可重试] "

func safeSubmitRetry(po *ent.ProcurementOrder) bool {
	return strings.HasPrefix(po.LastError, submitRetryPrefix) ||
		po.LastError == "adapter.acgfaka: 上游拒绝(0): 订单未完成，请检查库存、规格或余额后再试"
}

func (s *ProcureService) retrySubmission(ctx context.Context, po *ent.ProcurementOrder) error {
	if po.Status != procurementorder.StatusPending || !safeSubmitRetry(po) {
		return s.repo.MarkManual(ctx, po.ID, "上游单号缺失，请核实上游扣款和出货结果后人工补发")
	}
	if po.NextRetryAt.After(time.Now().UTC()) {
		return nil
	}
	if int(po.RetryCount) >= len(pollIntervals) {
		return s.repo.MarkManual(ctx, po.ID, "采购提交重试已耗尽，请检查库存、规格和余额后人工处理")
	}
	it, claimed, err := s.repo.claimSubmissionRetry(ctx, po)
	if errors.Is(err, ErrOrderNotPurchasable) {
		_, err := data.Client(ctx, s.repo.data).ProcurementOrder.Update().Where(procurementorder.ID(po.ID), procurementorder.StatusEQ(procurementorder.StatusPending)).SetStatus(procurementorder.StatusManual).SetLastError("原订单已关闭、取消或正在退款，已停止采购重试").ClearNextRetryAt().Save(ctx)
		return err
	}
	if err != nil || !claimed {
		return err
	}
	return s.submitClaimed(ctx, po, it)
}

// Consume the retry proof under the same order lock used by refunds. A crash
// after this commit is uncertain and cannot trigger another purchase.
func (r *ProcureRepo) claimSubmissionRetry(ctx context.Context, po *ent.ProcurementOrder) (it *ent.OrderItem, claimed bool, err error) {
	anchor, err := data.Client(ctx, r.data).OrderItem.Get(ctx, po.OrderItemID)
	if err != nil {
		return nil, false, err
	}
	err = data.PurchaseRetry(ctx, r.data, func(ctx context.Context) error {
		claimed = false
		c := data.Client(ctx, r.data)
		if err := c.Order.UpdateOneID(anchor.OrderID).AddVersion(0).Exec(ctx); err != nil {
			return err
		}
		var err error
		it, err = c.OrderItem.Get(ctx, po.OrderItemID)
		if err != nil {
			return err
		}
		o, err := c.Order.Get(ctx, anchor.OrderID)
		if err != nil {
			return err
		}
		if it.OrderID != anchor.OrderID || it.DeliveryKind != "card" || it.CanceledQuantity > 0 || it.FulfillmentStatus == "refunded" || it.FulfillmentStatus == "delivered" ||
			(o.Status != order.StatusPaid && o.Status != order.StatusFulfilling && o.Status != order.StatusPartiallyDelivered) {
			return ErrOrderNotPurchasable
		}
		busy, err := c.RefundOrder.Query().Where(refundorder.OrderID(o.ID), refundorder.StatusIn("created", "processing")).Exist(ctx)
		if err != nil {
			return err
		}
		if busy {
			return ErrOrderNotPurchasable
		}
		now := time.Now().UTC()
		n, err := c.ProcurementOrder.Update().Where(
			procurementorder.ID(po.ID), procurementorder.StatusEQ(procurementorder.StatusPending),
			procurementorder.RetryCountEQ(po.RetryCount), procurementorder.LastErrorEQ(po.LastError),
			procurementorder.Or(procurementorder.UpstreamOrderIDIsNil(), procurementorder.UpstreamOrderIDEQ("")),
			procurementorder.NextRetryAtLTE(now),
		).SetLastError("").SetLastPollAt(now).SetNextRetryAt(now.Add(5 * time.Minute)).Save(ctx)
		claimed = n == 1
		return err
	})
	return
}

func (r *ProcureRepo) touchPollAttempt(ctx context.Context, id uint64) error {
	now := time.Now().UTC()
	_, err := data.Client(ctx, r.data).ProcurementOrder.Update().Where(procurementorder.ID(id),
		procurementorder.StatusIn(procurementorder.StatusPending, procurementorder.StatusSubmitted, procurementorder.StatusPolling),
	).SetLastPollAt(now).SetNextRetryAt(now.Add(30 * time.Minute)).Save(ctx)
	return err
}
