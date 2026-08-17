package order

// P1-01 管理列表已售聚合：order_items × orders（paid 及之后状态）quantity 合计，
// 供 catalog 管理列表 SoldCount 展示（通道 A 端口消费）。

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
)

// SoldBatch 批量已售数量（一条 GROUP BY；无订单的商品无条目）。
func (uc *OrderUsecase) SoldBatch(ctx context.Context, productIDs []uint64) (map[uint64]int64, error) {
	out := make(map[uint64]int64, len(productIDs))
	if len(productIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ProductID uint64 `json:"product_id"`
		Total     int64  `json:"total"`
	}
	err := data.Client(ctx, uc.Data).OrderItem.Query().
		Where(
			orderitem.ProductIDIn(productIDs...),
			orderitem.HasOrderWith(order.StatusNotIn(
				order.StatusPendingPayment, order.StatusCanceled, order.StatusExpired,
			)),
		).
		GroupBy(orderitem.FieldProductID).
		Aggregate(ent.As(ent.Sum(orderitem.FieldQuantity), "total")).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ProductID] = r.Total
	}
	return out, nil
}
