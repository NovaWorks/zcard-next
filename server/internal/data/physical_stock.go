package data

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/physicalstockmovement"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
)

func PhysicalAvailable(ctx context.Context, c *ent.Client, p *ent.Product) (int64, error) {
	if !p.TrackInventory {
		return -1, nil
	}
	skus, e := c.ProductSku.Query().Where(productsku.ProductID(p.ID), productsku.SubsiteID(p.SubsiteID)).All(ctx)
	if e != nil {
		return 0, e
	}
	if len(skus) == 0 {
		return p.PhysicalStock, nil
	}
	var n int64
	for _, s := range skus {
		n += s.PhysicalStock
	}
	return n, nil
}

// MovePhysicalStock must join the caller's transaction. The unique reference and
// conditional stock update make compensation and request retries safe.
func MovePhysicalStock(ctx context.Context, d *Data, site, pid, sku, oid uint64, delta int64, ref, reason string) error {
	c := Client(ctx, d)
	exists, e := c.PhysicalStockMovement.Query().Where(physicalstockmovement.Reference(ref)).Exist(ctx)
	if e != nil {
		return e
	}
	if exists {
		return nil
	}
	if delta == 0 {
		return nil
	}
	var n int
	if sku == 0 {
		q := c.Product.Update().Where(product.ID(pid), product.SubsiteID(site), product.GoodsType("physical"))
		if delta < 0 {
			q.Where(product.PhysicalStockGTE(-delta))
		}
		n, e = q.AddPhysicalStock(delta).Save(ctx)
	} else {
		q := c.ProductSku.Update().Where(productsku.ID(sku), productsku.ProductID(pid), productsku.SubsiteID(site))
		if delta < 0 {
			q.Where(productsku.PhysicalStockGTE(-delta))
		}
		n, e = q.AddPhysicalStock(delta).Save(ctx)
	}
	if e != nil {
		return e
	}
	if n != 1 {
		return fmt.Errorf("order.INSUFFICIENT_STOCK: 实物库存不足或规格不存在")
	}
	return c.PhysicalStockMovement.Create().SetSubsiteID(site).SetProductID(pid).SetSkuID(sku).SetOrderID(oid).SetDelta(delta).SetReference(ref).SetReason(reason).Exec(ctx)
}
func AdjustPhysicalStock(ctx context.Context, d *Data, p *ent.Product, sku uint64, target int64) error {
	if target < 0 || target > 100000000 {
		return fmt.Errorf("实物可售库存必须在0至一亿之间")
	}
	old := p.PhysicalStock
	if sku == 0 && target != old {
		hasSKU, e := Client(ctx, d).ProductSku.Query().Where(productsku.ProductID(p.ID)).Exist(ctx)
		if e != nil {
			return e
		}
		if hasSKU {
			return fmt.Errorf("多规格实体商品请在规格中调整库存，不能修改商品级库存")
		}
	}
	if sku != 0 {
		s, e := Client(ctx, d).ProductSku.Get(ctx, sku)
		if e != nil {
			return e
		}
		old = s.PhysicalStock
	}
	return MovePhysicalStock(ctx, d, p.SubsiteID, p.ID, sku, 0, target-old, fmt.Sprintf("adjust:%d:%d:%d", p.ID, sku, p.LockVersion), "管理员调整可售库存")
}
func ReleasePhysicalStock(ctx context.Context, d *Data, oid uint64, reason string) error {
	rows, e := Client(ctx, d).OrderItem.Query().Where(orderitem.OrderID(oid), orderitem.GoodsType("physical")).All(ctx)
	if e != nil {
		return e
	}
	for _, it := range rows {
		// Stock reservations belong to the order snapshot. Changing the product's
		// inventory setting must not discard an older reservation or create one.
		if !it.InventoryTracked {
			continue
		}
		if e = MovePhysicalStock(ctx, d, it.SubsiteID, it.ProductID, it.SkuID, oid, int64(it.Quantity-it.CanceledQuantity), fmt.Sprintf("cancel:%d", it.ID), reason); e != nil {
			return e
		}
	}
	return nil
}
