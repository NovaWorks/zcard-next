package wallet

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
)

// DisplayReferences resolves business numbers without changing accounting idempotency keys.
// Only return order numbers owned by the ledger's user, including for legacy references.
func (r *WalletRepoImpl) DisplayReferences(ctx context.Context, rows []*ent.WalletTransaction) (map[uint64]string, error) {
	out := make(map[uint64]string, len(rows))
	orderIDs, refundIDs := []uint64{}, []uint64{}
	rowOrders, rowRefunds := map[uint64]uint64{}, map[uint64]uint64{}
	labels := map[string]string{"order_pay": "订单", "order_refund": "退款记录", "recharge": "充值支付记录", "giftcard": "礼品卡", "commission": "佣金记录", "commission_debt": "佣金欠款记录", "adjust": "调账记录"}
	for _, row := range rows {
		out[row.ID] = fmt.Sprintf("流水 #%d", row.ID)
		kind, value, found := strings.Cut(row.Reference, ":")
		if found && kind == "ticket_urgent" {
			out[row.ID] = "工单 " + value
		}
		id, err := strconv.ParseUint(value, 10, 64)
		if err == nil && id > 0 {
			if label := labels[kind]; label != "" {
				out[row.ID] = fmt.Sprintf("%s #%d", label, id)
			}
			if kind == "order_pay" {
				rowOrders[row.ID] = id
			}
			if kind == "order_refund" {
				rowRefunds[row.ID] = id
				refundIDs = append(refundIDs, id)
			}
		}
		if row.OrderID > 0 && (row.Type == "order_pay" || row.Type == "refund" || row.Type == "order_refund") {
			rowOrders[row.ID] = row.OrderID
		}
	}
	client := data.Client(ctx, r.data)
	if len(refundIDs) > 0 {
		refunds, err := client.RefundOrder.Query().Where(refundorder.IDIn(refundIDs...)).All(ctx)
		if err != nil {
			return nil, err
		}
		byID := map[uint64]uint64{}
		for _, refund := range refunds {
			byID[refund.ID] = refund.OrderID
		}
		for rowID, refundID := range rowRefunds {
			if id := byID[refundID]; id > 0 {
				rowOrders[rowID] = id
			}
		}
	}
	for _, id := range rowOrders {
		orderIDs = append(orderIDs, id)
	}
	if len(orderIDs) == 0 {
		return out, nil
	}
	orders, err := client.Order.Query().Where(order.IDIn(orderIDs...)).All(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[uint64]*ent.Order{}
	for _, o := range orders {
		byID[o.ID] = o
	}
	for _, row := range rows {
		if o := byID[rowOrders[row.ID]]; o != nil && o.UserID == row.UserID {
			out[row.ID] = o.OrderNo
		}
	}
	return out, nil
}
