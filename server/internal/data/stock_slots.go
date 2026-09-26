package data

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"time"
)

// StockSlot describes the actual fulfillment source of one locally sold SKU.
// A stale reference may schedule a probe, but must never prove saleability or recovery.
type StockSlot struct {
	SKUID                                uint64
	Name, Source, SourceKey, UpstreamSKU string
	Quantity, Reference                  int64
	CheckedAt                            time.Time
	Mapping                              *ent.SupplyMapping
}

func StockSlots(ctx context.Context, d *Data, p *ent.Product) ([]StockSlot, error) {
	c := Client(ctx, d)
	skus, err := c.ProductSku.Query().Where(productsku.ProductID(p.ID), productsku.SubsiteID(p.SubsiteID)).Order(ent.Asc(productsku.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(skus) == 0 {
		skus = []*ent.ProductSku{nil}
	}
	mappings, err := c.SupplyMapping.Query().Where(supplymapping.LocalProductID(p.ID), supplymapping.ConnectionID(p.UpstreamSourceID), supplymapping.UpstreamProduct(p.UpstreamProductCode)).All(ctx)
	if err != nil {
		return nil, err
	}
	bySKU := map[string]*ent.SupplyMapping{}
	for _, m := range mappings {
		bySKU[m.UpstreamSku] = m
	}
	out := make([]StockSlot, 0, len(skus))
	for _, sk := range skus {
		slot := StockSlot{Source: StockSource(p, sk), Quantity: -2, Reference: -2}
		if sk != nil {
			slot.SKUID = sk.ID
			slot.Name = sk.Name
			slot.UpstreamSKU = sk.UpstreamSkuID
		}
		key, _ := json.Marshal([]any{slot.Source, p.UpstreamSourceID, p.UpstreamProductCode, slot.UpstreamSKU})
		slot.SourceKey = fmt.Sprintf("%x", sha256.Sum256(key))
		if slot.Source == "upstream" {
			if sk == nil || slot.UpstreamSKU != "" {
				slot.Mapping = bySKU[slot.UpstreamSKU]
			}
			if m := slot.Mapping; m != nil {
				slot.CheckedAt = m.StockCheckedAt
				slot.Reference = int64(m.StockReference)
				if m.UpStock >= -1 {
					slot.Reference = int64(m.UpStock)
					if !m.StockCheckedAt.IsZero() && time.Since(m.StockCheckedAt) <= 5*time.Minute {
						slot.Quantity = int64(m.UpStock)
					}
				}
			}
		} else {
			slot.Quantity, err = LocalSKUStock(ctx, d, p, sk)
			if err != nil {
				return nil, err
			}
			slot.Reference = slot.Quantity
			slot.CheckedAt = time.Now().UTC()
		}
		out = append(out, slot)
	}
	return out, nil
}
func HasLowStock(ctx context.Context, d *Data, p *ent.Product, threshold int) (bool, error) {
	slots, err := StockSlots(ctx, d, p)
	if err != nil {
		return false, err
	}
	for _, s := range slots {
		if s.Quantity >= 0 && s.Quantity < int64(threshold) {
			return true, nil
		}
	}
	return false, nil
}
