package data

import (
	"context"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
)

// OrderProductSummary 批量展示订单商品，不向用户列表附带成本或上游凭据。
type OrderProductSummary struct {
	ProductID, SkuID uint64
	Name, SkuName    string
	Quantity         int32
}

func OrderProductSummaries(ctx context.Context, d *Data, orders []*ent.Order) (map[uint64][]OrderProductSummary, error) {
	out := make(map[uint64][]OrderProductSummary)
	if len(orders) == 0 {
		return out, nil
	}
	ids := make([]uint64, 0, len(orders))
	for _, o := range orders {
		ids = append(ids, o.ID)
	}
	items, err := Client(ctx, d).OrderItem.Query().Where(orderitem.OrderIDIn(ids...)).Order(ent.Asc(orderitem.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	pids := make([]uint64, 0, len(items))
	for _, it := range items {
		pids = append(pids, it.ProductID)
	}
	names := map[uint64]string{}
	if len(pids) > 0 {
		products, err := Client(ctx, d).Product.Query().Where(product.IDIn(pids...)).Select(product.FieldID, product.FieldName).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range products {
			names[p.ID] = p.Name
		}
	}
	for _, it := range items {
		name := names[it.ProductID]
		if name == "" {
			name = fmt.Sprintf("商品 #%d（已不可用）", it.ProductID)
		}
		out[it.OrderID] = append(out[it.OrderID], OrderProductSummary{ProductID: it.ProductID, SkuID: it.SkuID, Name: name, SkuName: it.SkuName, Quantity: it.Quantity})
	}
	return out, nil
}
