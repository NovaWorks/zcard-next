package data

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/shipment"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func LockPhysicalOrder(ctx context.Context, d *Data, o *ent.Order, allowRefunded ...bool) error {
	if o.CommerceVersion != 1 {
		return fmt.Errorf("该订单不支持实体履约")
	}
	switch o.Status {
	case order.StatusRefunded:
		if len(allowRefunded) == 0 || !allowRefunded[0] {
			return fmt.Errorf("订单已退清")
		}
	case order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered, order.StatusDelivered, order.StatusCompleted:
	default:
		return fmt.Errorf("订单尚未付款或已关闭")
	}
	busy, e := Client(ctx, d).RefundOrder.Query().Where(refundorder.OrderID(o.ID), refundorder.StatusIn("created", "processing")).Exist(ctx)
	if e != nil {
		return e
	}
	if busy {
		return fmt.Errorf("退款待核对，暂不能操作物流")
	}
	n, e := Client(ctx, d).Order.Update().Where(order.ID(o.ID), order.Version(o.Version), order.StatusEQ(o.Status)).AddVersion(1).Save(ctx)
	if e != nil {
		return e
	}
	if n != 1 {
		return fmt.Errorf("订单已变化，请刷新后重试")
	}
	return nil
}
func ShipmentsJSON(ctx context.Context, d *Data, oid uint64) (string, error) {
	rows, e := Client(ctx, d).Shipment.Query().Where(shipment.OrderID(oid)).Order(ent.Asc(shipment.FieldID)).All(ctx)
	if e != nil {
		return "", e
	}
	// Explicit public projection: no request keys, operator identity or stored address.
	out := []map[string]any{}
	for _, s := range rows {
		out = append(out, map[string]any{"id": s.ID, "carrier": s.Carrier, "tracking_no": s.TrackingNo, "items": s.Items, "status": s.Status, "created_at": s.CreatedAt.Unix(), "received_at": s.ReceivedAt})
	}
	b, e := json.Marshal(out)
	return string(b), e
}
func PhysicalOrderEvent(ctx context.Context, d *Data, o *ent.Order, event, actor string, id uint64, reason string) error {
	// The event column is limited in bytes, so preserve UTF-8 at the boundary.
	if len(reason) > 250 {
		reason = reason[:250]
		for !utf8.ValidString(reason) {
			reason = reason[:len(reason)-1]
		}
	}
	current, e := Client(ctx, d).Order.Get(ctx, o.ID)
	if e != nil {
		return e
	}
	return Client(ctx, d).OrderStatusEvent.Create().SetOrderID(o.ID).SetFromStatus(string(o.Status)).SetToStatus(string(current.Status)).SetEvent(event).SetOperator(orderstatusevent.Operator(actor)).SetOperatorID(id).SetReason(reason).Exec(ctx)
}

// RefreshPhysicalProgress aggregates durable item facts; refund amounts alone
// never imply a cancellation, shipment or inventory return.
func RefreshPhysicalProgress(ctx context.Context, d *Data, o *ent.Order) error {
	if o.CommerceVersion != 1 || o.Status == order.StatusCanceled || o.Status == order.StatusPendingPayment {
		return nil
	}
	rows, e := Client(ctx, d).OrderItem.Query().Where(orderitem.OrderID(o.ID)).All(ctx)
	if e != nil {
		return e
	}
	allDone, anyDelivery, allShipped := true, false, true
	allVirtualDone := true
	var physical, shipped, received int32
	for _, it := range rows {
		remaining := it.Quantity - it.CanceledQuantity
		if it.GoodsType == "physical" {
			physical += remaining
			shipped += it.ShippedQuantity
			received += it.ReceivedQuantity
			if it.ShippedQuantity < remaining {
				allShipped = false
			}
			if it.ReceivedQuantity < remaining {
				allDone = false
			}
			anyDelivery = anyDelivery || it.ShippedQuantity > 0
		} else if remaining > 0 {
			done := it.FulfillmentStatus == "delivered" || it.FulfillmentStatus == "refunded"
			allDone = allDone && done
			allVirtualDone = allVirtualDone && done
			anyDelivery = anyDelivery || done
		}
	}
	state := "pending"
	if physical == 0 {
		state = "canceled"
	} else if received >= physical {
		state = "received"
	} else if allShipped {
		state = "shipped"
	} else if shipped > 0 {
		state = "partial"
	}
	next := order.StatusFulfilling
	if anyDelivery {
		next = order.StatusPartiallyDelivered
	}
	if physical > 0 && allShipped && allVirtualDone {
		next = order.StatusDelivered
	}
	if allDone {
		next = order.StatusCompleted
	}
	if o.Status == order.StatusRefunded {
		next = order.StatusRefunded
	}
	if e = Client(ctx, d).Order.UpdateOneID(o.ID).SetShippingStatus(state).SetStatus(next).Exec(ctx); e != nil {
		return e
	}
	if next == order.StatusCompleted && o.Status != next && anyDelivery {
		if e = PhysicalNotification(ctx, d, o, "order.delivered", "delivered", nil); e != nil {
			return e
		}
	}
	if o.Status != next {
		if e = Client(ctx, d).OrderStatusEvent.Create().SetOrderID(o.ID).SetFromStatus(string(o.Status)).SetToStatus(string(next)).SetEvent("fulfillment_progress").SetOperator("system").Exec(ctx); e != nil {
			return e
		}
	}
	return nil
}
func ReceivePhysicalShipment(ctx context.Context, d *Data, no string, sid uint64, actor string, aid uint64) error {
	return Tx(ctx, d, func(ctx context.Context) error {
		c := Client(ctx, d)
		o, e := c.Order.Query().Where(order.OrderNo(no)).Only(ctx)
		if e != nil {
			return e
		}
		s, e := c.Shipment.Query().Where(shipment.ID(sid), shipment.OrderID(o.ID)).Only(ctx)
		if e != nil {
			return e
		}
		if s.Status == "received" {
			return nil
		}
		if e = LockPhysicalOrder(ctx, d, o, true); e != nil {
			return e
		}
		n, e := c.Shipment.Update().Where(shipment.ID(s.ID), shipment.Status("shipped")).SetStatus("received").SetReceivedAt(time.Now().Unix()).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return fmt.Errorf("包裹状态已变化")
		}
		for key, qty := range s.Items {
			id, e := strconv.ParseUint(key, 10, 64)
			if e != nil {
				return e
			}
			n, e := c.OrderItem.Update().Where(orderitem.ID(id), orderitem.OrderID(o.ID), orderitem.GoodsType("physical")).AddReceivedQuantity(qty).SetFulfillmentStatus("received").Save(ctx)
			if e != nil {
				return e
			}
			if n != 1 {
				return fmt.Errorf("包裹商品不存在")
			}
		}
		if e = PhysicalOrderEvent(ctx, d, o, "shipment_received", actor, aid, fmt.Sprintf("确认收到包裹 %d", s.ID)); e != nil {
			return e
		}
		return RefreshPhysicalProgress(ctx, d, o)
	})
}

// Event payloads include no postal address or query password.
func PhysicalNotification(ctx context.Context, d *Data, o *ent.Order, event, key string, values map[string]any) error {
	email := o.Contact
	if !strings.Contains(email, "@") {
		email = o.GuestContact
	}
	if o.UserID > 0 {
		u, e := Client(ctx, d).User.Get(ctx, o.UserID)
		if e != nil && !ent.IsNotFound(e) {
			return e
		}
		if u != nil {
			email = u.Email
		}
	}
	payload := map[string]any{"order_no": o.OrderNo, "order_id": o.ID, "subsite_id": o.SubsiteID, "user_id": o.UserID, "email": email}
	for k, v := range values {
		payload[k] = v
	}
	raw, e := json.Marshal(payload)
	if e != nil {
		return e
	}
	return NewOutboxWriter(d).Write(ctx, "fulfillment", event, o.OrderNo, "order:"+o.OrderNo+":"+key, raw)
}
