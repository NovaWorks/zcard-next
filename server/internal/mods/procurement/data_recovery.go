package procurement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderdelivery"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	fulfillmentport "github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

// Preserve the first encrypted receipt. Local delivery can be retried without buying again.
func (r *ProcureRepo) saveReceipt(ctx context.Context, id uint64, sealed [][]byte) error {
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		c := data.Client(ctx, r.data)
		po, err := r.Get(ctx, id)
		if err != nil {
			return err
		}
		if po.Status == procurementorder.StatusFulfilled {
			return nil
		}
		switch po.Status {
		case procurementorder.StatusPending, procurementorder.StatusSubmitted, procurementorder.StatusPolling:
		default:
			return ErrTransitionDenied
		}
		n, err := c.ProcurementOrder.Update().Where(procurementorder.ID(id), procurementorder.StatusEQ(po.Status)).SetStatus(procurementorder.StatusPolling).SetLastPollAt(time.Now().UTC()).Save(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConcurrentUpdate
		}
		existing, err := r.ReceivedContent(ctx, id)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			return nil
		}
		return r.AttachReceivedContent(ctx, id, sealed)
	})
}

func (s *ProcureService) deliverReceipt(ctx context.Context, id uint64, amount int64) error {
	return data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		po, err := s.repo.Get(ctx, id)
		if err != nil {
			return err
		}
		switch po.Status {
		case procurementorder.StatusPending, procurementorder.StatusSubmitted, procurementorder.StatusPolling, procurementorder.StatusFulfilled:
		default:
			return ErrTransitionDenied
		}
		oi, err := s.repo.OrderItemInfo(ctx, po.OrderItemID)
		if err != nil {
			return err
		}
		count, err := s.repo.deliveredCount(ctx, po.OrderItemID, oi.Quantity)
		if err != nil {
			return err
		}
		if count >= int(oi.Quantity) && po.Status == procurementorder.StatusFulfilled {
			return nil
		}
		if count > 0 && count < int(oi.Quantity) {
			return fmt.Errorf("采购商品已部分交付，请核实后转人工补发剩余数量")
		}
		sealed, err := s.repo.ReceivedContent(ctx, id)
		if err != nil {
			return err
		}
		if len(sealed) != int(oi.Quantity) || len(sealed) == 0 {
			return fmt.Errorf("上游卡密数量与购买数量不符，请核实后转人工补发")
		}
		items := make([]fulfillmentport.UpstreamDeliveryItem, 0, len(sealed))
		for _, ct := range sealed {
			plain, err := s.cipher.Open(ct, oi.ProductID, oi.SubsiteID)
			if err != nil {
				return fmt.Errorf("采购卡密解密失败，请核实后转人工")
			}
			items = append(items, fulfillmentport.UpstreamDeliveryItem{SealedContent: ct, ContentHash: s.cipher.ContentHash(plain)})
		}
		if err := s.attach.AttachUpstreamDelivery(ctx, oi.OrderID, po.OrderItemID, oi.ProductID, items); err != nil {
			return err
		}
		// Delivery and completion commit together; losing a race to manual handling rolls back delivery.
		n, err := data.Client(ctx, s.repo.data).ProcurementOrder.Update().Where(procurementorder.ID(id), procurementorder.StatusEQ(po.Status)).SetStatus(procurementorder.StatusFulfilled).SetLastError("").SetLastPollAt(time.Now().UTC()).Save(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConcurrentUpdate
		}
		if s.outbox != nil && po.Status != procurementorder.StatusFulfilled {
			raw, _ := json.Marshal(map[string]any{"procurement_id": id, "order_item_id": po.OrderItemID, "cards": len(items), "amount": amount})
			agg := fmt.Sprintf("proc:%d", id)
			return s.outbox.Write(ctx, "procurement", events.ProcurementFulfilled, agg, agg, raw)
		}
		return nil
	})
}

func (r *ProcureRepo) deliveredCount(ctx context.Context, itemID uint64, quantity int32) (int, error) {
	rows, err := data.Client(ctx, r.data).OrderDelivery.Query().Where(orderdelivery.ItemID(itemID)).All(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, row := range rows {
		if row.DeliveredQuantity > 0 {
			count += int(row.DeliveredQuantity)
		} else if row.DeliveredMode == orderdelivery.DeliveredModeDirect || len(row.Logistics) > 0 {
			return int(quantity), nil
		} else {
			count++
		}
	}
	return count, nil
}

// Lock the order before procurement, matching delivery/refunds. Only reopen an
// old fulfilled receipt when the buyer still lacks delivery and can receive it.
func (r *ProcureRepo) lockManualRecovery(ctx context.Context, po *ent.ProcurementOrder) error {
	c := data.Client(ctx, r.data)
	it, err := c.OrderItem.Get(ctx, po.OrderItemID)
	if ent.IsNotFound(err) && po.Status != procurementorder.StatusFulfilled {
		return nil
	}
	if err != nil {
		return err
	}
	o, err := c.Order.Get(ctx, it.OrderID)
	if err != nil {
		return err
	}
	switch o.Status {
	case order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered:
	default:
		return fmt.Errorf("当前订单状态不允许转人工发货")
	}
	n, err := c.Order.Update().Where(order.ID(o.ID), order.Version(o.Version), order.StatusEQ(o.Status)).AddVersion(1).Save(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConcurrentUpdate
	}
	processing, err := c.RefundOrder.Query().Where(refundorder.OrderID(o.ID), refundorder.StatusEQ(refundorder.StatusProcessing)).Exist(ctx)
	if err != nil {
		return err
	}
	if processing {
		return fmt.Errorf("退款处理中，请先核实退款结果")
	}
	count, err := r.deliveredCount(ctx, it.ID, it.Quantity)
	if err != nil {
		return err
	}
	if count >= int(it.Quantity) {
		return fmt.Errorf("该商品已完成交付，无需转人工")
	}
	return nil
}

// Record missing tasks for investigation, never blindly resubmit upstream purchases.
func (r *ProcureRepo) recordMissing(ctx context.Context, itemID uint64) error {
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		if _, err := r.GetByOrderItem(ctx, itemID); err == nil {
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		c := data.Client(ctx, r.data)
		it, err := c.OrderItem.Get(ctx, itemID)
		if err != nil {
			return err
		}
		if it.FulfillmentType != orderitem.FulfillmentTypeUpstream {
			return nil
		}
		o, err := c.Order.Get(ctx, it.OrderID)
		if err != nil {
			return err
		}
		switch o.Status {
		case order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered:
		default:
			return nil
		}
		count, err := r.deliveredCount(ctx, it.ID, it.Quantity)
		if err != nil {
			return err
		}
		if count >= int(it.Quantity) {
			return nil
		}
		var connection uint64
		var code string
		if p, e := c.Product.Get(ctx, it.ProductID); e == nil {
			connection = p.UpstreamSourceID
			code = p.UpstreamProductCode
		} else if !ent.IsNotFound(e) {
			return e
		}
		po, err := r.CreatePending(ctx, it.ID, connection, code, it.Quantity, "manual", o.OrderNo)
		if err != nil {
			return err
		}
		return r.MarkManual(ctx, po.ID, "未能生成有效采购任务，请核实上游扣款和出货结果后人工补发；系统不会重复采购")
	})
}

func (r *ProcureRepo) reconcileMissing(ctx context.Context) error {
	c := data.Client(ctx, r.data)
	// Allow five minutes for the normal payment consumer to create procurement.
	ids, err := c.OrderItem.Query().Where(orderitem.FulfillmentTypeEQ(orderitem.FulfillmentTypeUpstream), orderitem.FulfillmentStatusNEQ("delivered"), func(s *sql.Selector) {
		po := sql.Table(procurementorder.Table)
		o := sql.Table(order.Table)
		s.Where(sql.NotIn(s.C(orderitem.FieldID), sql.Select(po.C(procurementorder.FieldOrderItemID)).From(po)))
		s.Where(sql.In(s.C(orderitem.FieldOrderID), sql.Select(o.C(order.FieldID)).From(o).Where(sql.And(sql.In(o.C(order.FieldStatus), "paid", "fulfilling", "partially_delivered"), sql.LT(o.C(order.FieldPaidAt), time.Now().UTC().Add(-5*time.Minute))))))
	}).Order(ent.Asc(orderitem.FieldID)).Limit(100).IDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := r.recordMissing(ctx, id); err != nil && !errors.Is(err, ErrDuplicatePurchase) {
			return err
		}
	}
	return nil
}

func (r *ProcureRepo) recordDeliveryFailure(ctx context.Context, id uint64) error {
	return data.Client(ctx, r.data).ProcurementOrder.Update().Where(procurementorder.ID(id), procurementorder.StatusEQ(procurementorder.StatusPolling)).AddRetryCount(1).SetNextRetryAt(time.Now().UTC().Add(time.Minute)).SetLastError("上游已返回结果，本地交付失败，请重试交付或核实后转人工").Exec(ctx)
}
