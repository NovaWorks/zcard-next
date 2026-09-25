package data

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productcontrol"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
)

// FulfillmentMode resolves a snapshot; a SKU never changes the supplier identity.
func FulfillmentMode(p *ent.Product, sku *ent.ProductSku) string {
	switch StockSource(p, sku) {
	case "upstream":
		return "upstream"
	case "reuse":
		return "reuse"
	case "manual":
		return "manual"
	}
	return "auto"
}

// ManualAvailable counts reservations and completed sales against the product's shared quota.
// Call while holding the product write lock when admitting a new order.
func ManualAvailable(ctx context.Context, c *ent.Client, p *ent.Product, currentRead ...bool) (int64, error) {
	if p.ManualStock < 0 {
		return -1, nil
	}
	locked := len(currentRead) > 0 && currentRead[0]
	q := c.OrderItem.Query().Where(orderitem.ProductID(p.ID), orderitem.SubsiteID(p.SubsiteID), orderitem.FulfillmentTypeEQ(orderitem.FulfillmentTypeManual))
	if locked {
		q = q.ForUpdate()
	} else {
		q = q.Where(orderitem.HasOrderWith(order.StatusNotIn(order.StatusCanceled, order.StatusExpired, order.StatusRefunded)))
	}
	rows, err := q.All(ctx)
	if err != nil {
		return 0, err
	}
	active := map[uint64]bool{}
	if locked && len(rows) > 0 {
		ids := make([]uint64, 0, len(rows))
		for _, it := range rows {
			ids = append(ids, it.OrderID)
		}
		// Locking reads see committed rows after waiting for the product lock, even under MySQL REPEATABLE READ.
		orders, err := c.Order.Query().Where(order.IDIn(ids...), order.StatusNotIn(order.StatusCanceled, order.StatusExpired, order.StatusRefunded)).ForUpdate().All(ctx)
		if err != nil {
			return 0, err
		}
		for _, o := range orders {
			active[o.ID] = true
		}
	}
	n := p.ManualStock
	for _, it := range rows {
		if !locked || active[it.OrderID] {
			n -= int64(it.Quantity)
		}
	}
	if n < 0 {
		n = 0
	}
	return n, nil
}

// RejectServiceProduct protects synchronous card-only purchasing entrypoints.
func RejectServiceProduct(ctx context.Context, c *ent.Client, p *ent.Product) error {
	if p.FulfillmentMode == "manual" {
		return fmt.Errorf("该入口不支持人工服务商品")
	}
	manual, err := c.ProductSku.Query().Where(productsku.ProductID(p.ID), productsku.FulfillmentMode("manual")).Exist(ctx)
	if err != nil {
		return err
	}
	if manual {
		return fmt.Errorf("该入口不支持含人工规格的商品")
	}
	forms, err := c.ProductControl.Query().Where(productcontrol.ProductID(p.ID)).Exist(ctx)
	if err != nil {
		return err
	}
	if forms {
		return fmt.Errorf("该商品需要填写资料，请在商城下单")
	}
	return nil
}
