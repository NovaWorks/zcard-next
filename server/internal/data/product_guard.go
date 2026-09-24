package data

import (
	"context"
	stderrors "errors"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
)

var ErrProductLocked = errors.Conflict("catalog.PRODUCT_LOCKED", "商品已锁定，请先解锁后再操作")

func IsProductLocked(err error) bool { return stderrors.Is(err, ErrProductLocked) }

// GuardProductWrite must run inside data.Tx. The conditional write serializes
// maintenance with SetProductLock; order reservations/fulfillment do not use it.
func GuardProductWrite(ctx context.Context, d *Data, id uint64) (*ent.Product, error) {
	c := Client(ctx, d)
	tenant := tenancy.FromContext(ctx).SubsiteID
	if d.Dialect == db.SQLite {
		// SQLite serializes writers at database level, including a zero-match update.
		if _, err := c.Product.Update().Where(product.ID(id), product.SubsiteID(tenant), product.StatusGTE(0), product.IsLocked(false)).AddLockVersion(0).Save(ctx); err != nil {
			return nil, err
		}
	}
	q := c.Product.Query().Where(product.ID(id), product.SubsiteID(tenant), product.StatusGTE(0))
	if d.Dialect != db.SQLite {
		q.ForUpdate()
	}
	p, err := q.Only(ctx)

	if ent.IsNotFound(err) {
		return nil, errors.NotFound("catalog.PRODUCT_NOT_FOUND", "商品不存在或已删除")
	}
	if err != nil {
		return nil, err
	}
	if p.IsLocked {
		return nil, ErrProductLocked
	}
	return p, nil
}
