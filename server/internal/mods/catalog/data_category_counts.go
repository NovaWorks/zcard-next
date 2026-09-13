package catalog

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// CategoryProductCounts matches the product list: include descendants and all non-deleted statuses.
func (r *ProductRepoImpl) CategoryProductCounts(ctx context.Context, categories []*ent.Category) (map[uint64]int64, error) {
	var rows []struct {
		CategoryID uint64 `json:"category_id"`
		Count      int64  `json:"count"`
	}
	e := data.Client(ctx, r.data).Product.Query().Where(product.SubsiteID(tenancy.FromContext(ctx).SubsiteID), product.StatusGTE(0)).GroupBy(product.FieldCategoryID).Aggregate(ent.Count()).Scan(ctx, &rows)
	if e != nil {
		return nil, e
	}
	parents := map[uint64]uint64{}
	for _, c := range categories {
		parents[c.ID] = c.ParentID
	}
	counts := map[uint64]int64{}
	for _, row := range rows {
		seen := map[uint64]bool{}
		for id := row.CategoryID; id != 0 && !seen[id]; id = parents[id] {
			if _, ok := parents[id]; !ok {
				break
			}
			seen[id] = true
			counts[id] += row.Count
		}
	}
	return counts, nil
}
