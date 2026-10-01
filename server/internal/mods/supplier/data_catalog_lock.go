package supplier

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// MySQL reports changed rows for a no-op UPDATE, which can be zero for an
// existing record. Lock and read the record instead of using that count as proof
// of existence. A locking read also sees committed deletions after waiting.
func (r *SupplierRepoImpl) lockCatalogProduct(ctx context.Context, id uint64, activeOnly bool) (bool, error) {
	c := data.Client(ctx, r.data)
	q := c.Product.Query().Where(product.ID(id), product.SubsiteID(tenancy.FromContext(ctx).SubsiteID), product.StatusGTE(0))
	if activeOnly {
		q.Where(product.Status(1))
	}
	if r.data.Dialect == db.SQLite {
		if e := c.Product.Update().Where(product.ID(id), product.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).AddLockVersion(0).Exec(ctx); e != nil {
			return false, e
		}
	} else {
		q.ForUpdate()
	}
	_, e := q.Only(ctx)
	if ent.IsNotFound(e) {
		return false, nil
	}
	return e == nil, e
}
func (r *SupplierRepoImpl) lockCatalogCategory(ctx context.Context, id uint64) (bool, error) {
	c := data.Client(ctx, r.data)
	q := c.Category.Query().Where(category.ID(id), category.SubsiteID(tenancy.FromContext(ctx).SubsiteID))
	if r.data.Dialect == db.SQLite {
		if e := c.Category.Update().Where(category.ID(id), category.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).AddPlacementVersion(0).Exec(ctx); e != nil {
			return false, e
		}
	} else {
		q.ForUpdate()
	}
	_, e := q.Only(ctx)
	if ent.IsNotFound(e) {
		return false, nil
	}
	return e == nil, e
}
