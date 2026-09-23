package data

import (
	"context"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
)

// ProductStockSnapshot separates a display quantity from its freshness.
// -1 means unlimited; -2 means unknown (missing mapping or failed read).
type ProductStockSnapshot struct {
	Quantity  int64
	CheckedAt time.Time
	Status    string // current | stale | unknown
}

func (s ProductStockSnapshot) Available() int64 {
	if s.Status != "current" {
		return -2
	}
	return s.Quantity
}

// ProductStocks resolves stock by fulfillment source for authorized products.
// Local cards count only available rows in the same subsite; upstream products
// use their current product-level mapping, never the local pool or stale counts.
func ProductStocks(ctx context.Context, d *Data, products []*ent.Product) (map[uint64]int64, error) {
	snapshots, err := ProductStockSnapshots(ctx, d, products)
	if err != nil {
		return nil, err
	}
	out := make(map[uint64]int64, len(snapshots))
	for id, snapshot := range snapshots {
		out[id] = snapshot.Available()
	}
	return out, nil
}

// ProductStockSnapshots keeps old quantities for display without treating them as available stock.
func ProductStockSnapshots(ctx context.Context, d *Data, products []*ent.Product) (map[uint64]ProductStockSnapshot, error) {
	out := make(map[uint64]ProductStockSnapshot, len(products))
	client := Client(ctx, d)
	// Bound IN clauses for large supplier catalogs on all supported databases.
	for start := 0; start < len(products); start += 500 {
		end := start + 500
		if end > len(products) {
			end = len(products)
		}
		localIDs, upIDs := []uint64{}, []uint64{}
		byID := map[uint64]*ent.Product{}
		for _, p := range products[start:end] {
			byID[p.ID] = p
			switch {
			case p.FulfillmentMode == "manual" && p.UpstreamSourceID == 0:
				n, err := ManualAvailable(ctx, client, p)
				if err != nil {
					return nil, err
				}
				out[p.ID] = ProductStockSnapshot{Quantity: n, Status: "current"}
			case p.UpstreamSourceID > 0:
				out[p.ID] = ProductStockSnapshot{Quantity: -2, Status: "unknown"}
				upIDs = append(upIDs, p.ID)
			case p.StockType == "card":
				out[p.ID] = ProductStockSnapshot{Quantity: 0, Status: "current"}
				localIDs = append(localIDs, p.ID)
			default:
				out[p.ID] = ProductStockSnapshot{Quantity: -1, Status: "current"}
			}
		}
		if len(localIDs) > 0 {
			var counts []struct {
				ProductID uint64 `json:"product_id"`
				SubsiteID uint64 `json:"subsite_id"`
				Count     int64  `json:"count"`
			}
			err := client.Card.Query().Where(card.ProductIDIn(localIDs...), card.StatusEQ(card.StatusAvailable)).GroupBy(card.FieldProductID, card.FieldSubsiteID).Aggregate(ent.Count()).Scan(ctx, &counts)
			if err != nil {
				return nil, err
			}
			for _, c := range counts {
				if p := byID[c.ProductID]; p != nil && p.SubsiteID == c.SubsiteID {
					out[p.ID] = ProductStockSnapshot{Quantity: c.Count, Status: "current"}
				}
			}
		}
		// Mixed SKU products expose the sum of automatic stock and one shared manual quota.
		ids := make([]uint64, 0, len(byID))
		for id := range byID {
			ids = append(ids, id)
		}
		skus, err := client.ProductSku.Query().Where(productsku.ProductIDIn(ids...)).All(ctx)
		if err != nil {
			return nil, err
		}
		grouped := map[uint64][]*ent.ProductSku{}
		for _, sku := range skus {
			grouped[sku.ProductID] = append(grouped[sku.ProductID], sku)
		}
		for _, p := range byID {
			if p.UpstreamSourceID > 0 {
				continue
			}
			manual := p.FulfillmentMode == "manual" && len(grouped[p.ID]) == 0
			autoIDs := []uint64{}
			if !manual && len(grouped[p.ID]) == 0 {
				autoIDs = append(autoIDs, 0)
			}
			for _, sku := range grouped[p.ID] {
				if FulfillmentMode(p, sku) == "manual" {
					manual = true
				} else {
					autoIDs = append(autoIDs, sku.ID)
				}
			}
			if !manual {
				continue
			}
			n, err := ManualAvailable(ctx, client, p)
			if err != nil {
				return nil, err
			}
			if n >= 0 && len(autoIDs) > 0 {
				if p.StockType != "card" {
					n = -1
				} else {
					count, err := client.Card.Query().Where(card.ProductID(p.ID), card.SubsiteID(p.SubsiteID), card.StatusEQ(card.StatusAvailable), card.SkuIDIn(autoIDs...)).Count(ctx)
					if err != nil {
						return nil, err
					}
					n += int64(count)
				}
			}
			out[p.ID] = ProductStockSnapshot{Quantity: n, Status: "current"}
		}

		if len(upIDs) > 0 {
			rows, err := client.SupplyMapping.Query().Where(supplymapping.LocalProductIDIn(upIDs...), supplymapping.UpstreamSkuEQ("")).All(ctx)
			if err != nil {
				return nil, err
			}
			for _, m := range rows {
				p := byID[m.LocalProductID]
				if p != nil && p.UpstreamSourceID == m.ConnectionID && p.UpstreamProductCode == m.UpstreamProduct {
					snapshot := ProductStockSnapshot{Quantity: -2, CheckedAt: m.StockCheckedAt, Status: "unknown"}
					if m.StockReference >= -1 && !m.StockReferenceAt.IsZero() {
						snapshot = ProductStockSnapshot{Quantity: int64(m.StockReference), CheckedAt: m.StockReferenceAt, Status: "stale"}
					}
					if m.UpStock >= -1 && !m.StockCheckedAt.IsZero() {
						snapshot.Quantity = int64(m.UpStock)
						snapshot.Status = "stale"
						if time.Since(m.StockCheckedAt) <= 5*time.Minute {
							snapshot.Status = "current"
						}
					}
					out[p.ID] = snapshot
				}
			}
		}
	}
	return out, nil
}
