package fulfillment

import (
	"context"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderdelivery"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
)

// Lock the same order row used by refunds before creating any delivery records.
func (r *DeliveryRepoImpl) lockDeliveryOrder(ctx context.Context, o *ent.Order) error {
	switch o.Status {
	case order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered:
	default:
		return fmt.Errorf("该订单当前状态不允许发货")
	}
	n, err := data.Client(ctx, r.data).Order.Update().Where(order.ID(o.ID), order.StatusEQ(o.Status), order.Version(o.Version)).AddVersion(1).Save(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("订单已变化，请刷新后重试")
	}
	return nil
}

// Count actual delivery records, including legacy local cards with item_id=0.
// A direct link or a logistics record fulfills its entire item.
func (r *DeliveryRepoImpl) deliveredQuantities(ctx context.Context, orderID uint64, items []*ent.OrderItem) (map[uint64]int, error) {
	client := data.Client(ctx, r.data)
	rows, err := client.OrderDelivery.Query().Where(orderdelivery.OrderID(orderID)).Order(ent.Asc(orderdelivery.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[uint64]int{}
	for _, row := range rows {
		id := row.ItemID
		if id == 0 && row.CardID != 0 {
			c, e := client.Card.Get(ctx, row.CardID)
			if ent.IsNotFound(e) {
				continue
			}
			if e != nil {
				return nil, e
			}
			for _, it := range items {
				if it.ProductID == c.ProductID && it.SkuID == c.SkuID && counts[it.ID] < int(it.Quantity) {
					id = it.ID
					break
				}
			}
		}
		for _, it := range items {
			if it.ID != id {
				continue
			}
			if row.DeliveredMode == orderdelivery.DeliveredModeDirect || len(row.Logistics) > 0 {
				counts[id] = int(it.Quantity)
			} else {
				counts[id]++
			}
		}
	}
	return counts, nil
}

// Every item (local, manual and upstream) must be delivered before the order is.
func (r *DeliveryRepoImpl) updateDeliveryProgress(ctx context.Context, o *ent.Order, actor string, actorID uint64, remark string) error {
	client := data.Client(ctx, r.data)
	items, err := client.OrderItem.Query().Where(orderitem.OrderID(o.ID)).Order(ent.Asc(orderitem.FieldID)).All(ctx)
	if err != nil {
		return err
	}
	counts, err := r.deliveredQuantities(ctx, o.ID, items)
	if err != nil {
		return err
	}
	all, any := len(items) > 0, false
	for _, it := range items {
		n := counts[it.ID]
		if n >= int(it.Quantity) {
			if err := client.OrderItem.UpdateOneID(it.ID).SetFulfillmentStatus("delivered").Exec(ctx); err != nil {
				return err
			}
		} else {
			all = false
		}
		any = any || n > 0
	}
	next := order.StatusFulfilling
	if all {
		next = order.StatusDelivered
	} else if any {
		next = order.StatusPartiallyDelivered
	}
	if err := client.Order.UpdateOneID(o.ID).SetStatus(next).Exec(ctx); err != nil {
		return err
	}
	event := "fulfilling"
	if next == order.StatusDelivered {
		event = "delivered"
	} else if next == order.StatusPartiallyDelivered {
		event = "partially_delivered"
	}
	if actor == "admin" {
		event = "manual_delivered"
	}
	if o.Status == next && actor != "admin" {
		return nil
	}
	return client.OrderStatusEvent.Create().SetOrderID(o.ID).SetFromStatus(string(o.Status)).SetToStatus(string(next)).SetEvent(event).SetOperator(orderstatusevent.Operator(actor)).SetOperatorID(actorID).SetReason(remark).Exec(ctx)
}
