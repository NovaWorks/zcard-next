package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderdelivery"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productdeliverysource"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"time"
)

func DeliverySourceKey(tenant, productID, skuID uint64) string {
	return fmt.Sprintf("%d:%d:%d", tenant, productID, skuID)
}
func ConnectionRevision(c *ent.SupplyConnection) string {
	b, _ := json.Marshal([]any{c.Driver, c.BaseURL, c.Credentials, c.Status, c.ExchangeRate})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// StockSource separates provenance from the source actually used to fulfill a SKU.
func StockSource(p *ent.Product, sku *ent.ProductSku) string {
	mode := p.FulfillmentMode
	if sku != nil && sku.FulfillmentMode != "" && sku.FulfillmentMode != "follow" {
		mode = sku.FulfillmentMode
	}
	if mode == "local" || mode == "reuse" {
		return mode
	}
	if p.UpstreamSourceID > 0 {
		return "upstream"
	}
	if mode == "manual" {
		return "manual"
	}
	return "local"
}
func DeliverySKU(ctx context.Context, c *ent.Client, p *ent.Product, id uint64) (*ent.ProductSku, error) {
	if id == 0 {
		return nil, nil
	}
	return c.ProductSku.Query().Where(productsku.ID(id), productsku.ProductID(p.ID), productsku.SubsiteID(p.SubsiteID)).Only(ctx)
}
func CurrentDeliverySource(ctx context.Context, c *ent.Client, p *ent.Product, skuID uint64) (*ent.ProductDeliverySource, error) {
	return c.ProductDeliverySource.Query().Where(productdeliverysource.CurrentKey(DeliverySourceKey(p.SubsiteID, p.ID, skuID))).Only(ctx)
}
func SourceAdmissible(s *ent.ProductDeliverySource) bool {
	return (s.Status == "ready" && len(s.Content) > 0 || s.Status == "empty" || s.Status == "submitting" || s.Status == "polling") && (s.ExpiresAt == 0 || s.ExpiresAt > time.Now().Unix())
}

// Refunded, already delivered content still consumed an allocation.
func SourceAllocations(ctx context.Context, c *ent.Client, id uint64, currentRead ...bool) (int, error) {
	q := c.OrderItem.Query().Where(orderitem.DeliverySourceID(id))
	locked := len(currentRead) > 0 && currentRead[0]
	if locked {
		q = q.ForUpdate()
	}
	items, err := q.All(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	// Bounded lookups retain deliveries even when a later refund changes item status.
	for start := 0; start < len(items); start += 300 {
		end := min(start+300, len(items))
		orderIDs := []uint64{}
		itemIDs := []uint64{}
		for _, it := range items[start:end] {
			orderIDs = append(orderIDs, it.OrderID)
			itemIDs = append(itemIDs, it.ID)
		}
		oq := c.Order.Query().Where(order.IDIn(orderIDs...))
		dq := c.OrderDelivery.Query().Where(orderdelivery.ItemIDIn(itemIDs...))
		if locked {
			oq = oq.ForUpdate()
			dq = dq.ForUpdate()
		}
		orders, e := oq.All(ctx)
		if e != nil {
			return 0, e
		}
		deliveries, e := dq.All(ctx)
		if e != nil {
			return 0, e
		}
		active := map[uint64]bool{}
		sent := map[uint64]bool{}
		for _, o := range orders {
			active[o.ID] = o.Status != "canceled" && o.Status != "expired" && o.Status != "refunded"
		}
		for _, d := range deliveries {
			sent[d.ItemID] = true
		}
		for _, it := range items[start:end] {
			if sent[it.ID] || active[it.OrderID] && it.FulfillmentStatus != "refunded" {
				count++
			}
		}
	}
	return count, nil
}
func AdmitDeliverySource(ctx context.Context, d *Data, p *ent.Product, skuID uint64) (uint64, error) {
	c := Client(ctx, d)
	if err := c.Product.UpdateOneID(p.ID).AddLockVersion(0).Exec(ctx); err != nil {
		return 0, err
	}
	s, err := CurrentDeliverySource(ctx, c, p, skuID)
	if err != nil || !SourceAdmissible(s) {
		return 0, fmt.Errorf("该规格的复用内容未配置、已暂停或失效，请联系商家")
	}
	if s.MaxDeliveries > 0 {
		n, e := SourceAllocations(ctx, c, s.ID, d.Dialect.Capabilities().SupportsSkipLocked)
		if e != nil {
			return 0, e
		}
		if int64(n) >= s.MaxDeliveries {
			return 0, fmt.Errorf("该规格已达到可发放次数上限")
		}
	}
	return s.ID, nil
}
func LocalSKUStock(ctx context.Context, d *Data, p *ent.Product, sku *ent.ProductSku) (int64, error) {
	c := Client(ctx, d)
	id := uint64(0)
	if sku != nil {
		id = sku.ID
	}
	switch StockSource(p, sku) {
	case "reuse":
		s, err := CurrentDeliverySource(ctx, c, p, id)
		if ent.IsNotFound(err) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		if !SourceAdmissible(s) {
			return 0, nil
		}
		if s.MaxDeliveries == 0 {
			return -1, nil
		}
		n, e := SourceAllocations(ctx, c, s.ID)
		return max(0, s.MaxDeliveries-int64(n)), e
	case "manual":
		return ManualAvailable(ctx, c, p)
	case "local":
		if p.StockType != "card" {
			return -1, nil
		}
		q := c.Card.Query().Where(card.ProductID(p.ID), card.SubsiteID(p.SubsiteID), card.StatusEQ(card.StatusAvailable))
		if id == 0 {
			q = q.Where(card.Or(card.SkuIDIsNil(), card.SkuID(0)))
		} else {
			q = q.Where(card.SkuID(id))
		}
		n, e := q.Count(ctx)
		return int64(n), e
	}
	return -2, nil
}
func HasLocalDelivery(ctx context.Context, c *ent.Client, p *ent.Product) (bool, error) {
	if p.FulfillmentMode == "local" || p.FulfillmentMode == "reuse" {
		return true, nil
	}
	return c.ProductSku.Query().Where(productsku.ProductID(p.ID), productsku.FulfillmentModeIn("local", "reuse")).Exist(ctx)
}

var ErrLocalDeliveryProtected = errors.New("商品已配置本地发货，已跳过上游维护")

// The local policy is operator-owned; upstream maintenance must not rebuild it.
func GuardUpstreamDelivery(ctx context.Context, c *ent.Client, p *ent.Product) error {
	yes, err := HasLocalDelivery(ctx, c, p)
	if err != nil {
		return err
	}
	if yes {
		return ErrLocalDeliveryProtected
	}
	return nil
}
func ProductForDelivery(ctx context.Context, c *ent.Client, tenant, id uint64) (*ent.Product, error) {
	return c.Product.Query().Where(product.ID(id), product.SubsiteID(tenant)).Only(ctx)
}
