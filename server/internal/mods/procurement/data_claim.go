package procurement

import (
	"context"
	"errors"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
)

var ErrOrderNotPurchasable = errors.New("订单已关闭或商品已取消，不能采购")

// Persist the in-flight claim under the same order lock used by refunds. The
// remote call happens after commit; an ambiguous claim must be reconciled first.
func (r *ProcureRepo) claimPurchase(ctx context.Context, itemID, connectionID uint64, code string, qty int32, strategy, trace string, sku ...string) (out *ent.ProcurementOrder, err error) {
	anchor, e := data.Client(ctx, r.data).OrderItem.Get(ctx, itemID)
	if e != nil {
		return nil, e
	}
	err = data.PurchaseRetry(ctx, r.data, func(ctx context.Context) error {
		c := data.Client(ctx, r.data)
		// Acquire a write lock before the first transaction snapshot (SQLite WAL
		// cannot upgrade a stale read snapshot). The provider is called after commit.
		if e := c.Order.UpdateOneID(anchor.OrderID).AddVersion(0).Exec(ctx); e != nil {
			return e
		}
		it, e := c.OrderItem.Get(ctx, itemID)
		if e != nil {
			return e
		}
		if it.OrderID != anchor.OrderID {
			return ErrOrderNotPurchasable
		}
		o, e := c.Order.Get(ctx, it.OrderID)
		if e != nil {
			return e
		}
		if o.Status != order.StatusPaid && o.Status != order.StatusFulfilling && o.Status != order.StatusPartiallyDelivered {
			return ErrOrderNotPurchasable
		}
		n, e := c.Order.Update().Where(order.ID(o.ID), order.Version(o.Version), order.StatusEQ(o.Status)).AddVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConcurrentUpdate
		}
		it, e = c.OrderItem.Get(ctx, itemID)
		if e != nil {
			return e
		}
		if it.DeliveryKind != "card" {
			return ErrOrderNotPurchasable
		}
		if it.CanceledQuantity > 0 || it.FulfillmentStatus == "refunded" || it.FulfillmentStatus == "delivered" {
			return ErrOrderNotPurchasable
		}
		busy, e := c.RefundOrder.Query().Where(refundorder.OrderID(o.ID), refundorder.StatusIn("created", "processing")).Exist(ctx)
		if e != nil {
			return e
		}
		if busy {
			return fmt.Errorf("退款待核对，不能采购")
		}
		out, e = r.CreatePending(ctx, itemID, connectionID, code, qty, strategy, trace, sku...)
		return e
	})
	return
}
