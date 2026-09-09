package data

import (
	"context"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
)

// ProductStocks resolves display/filter stock by fulfillment source. Callers pass
// authorized products. Local cards count only available rows in the same subsite;
// upstream products use their current product-level mapping, never the local pool.
// -1 means unlimited; -2 means unknown (missing mapping or failed read).
func ProductStocks(ctx context.Context, d *Data, products []*ent.Product) (map[uint64]int64, error) {
	out := make(map[uint64]int64, len(products))
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
			case p.UpstreamSourceID > 0:
				out[p.ID] = -2
				upIDs = append(upIDs, p.ID)
			case p.StockType == "card":
				out[p.ID] = 0
				localIDs = append(localIDs, p.ID)
			default:
				out[p.ID] = -1
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
					out[p.ID] = c.Count
				}
			}
		}
		if len(upIDs) > 0 {
			rows, err := client.SupplyMapping.Query().Where(supplymapping.LocalProductIDIn(upIDs...), supplymapping.UpstreamSkuEQ("")).All(ctx)
			if err != nil {
				return nil, err
			}
			for _, m := range rows {
				p := byID[m.LocalProductID]
				if p != nil && p.UpstreamSourceID == m.ConnectionID && p.UpstreamProductCode == m.UpstreamProduct {
					if !m.StockCheckedAt.IsZero() && time.Since(m.StockCheckedAt) <= 5*time.Minute {
						out[p.ID] = int64(m.UpStock)
					}
				}
			}
		}
	}
	return out, nil
}
