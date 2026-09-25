package catalog

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
	"slices"
	"time"
)

func (s *AdminCatalogService) SetProductLock(ctx context.Context, req *adminv1.SetProductLockRequest) (out *adminv1.AdminProduct, err error) {
	actor, err := batchActor(ctx)
	if err != nil {
		return nil, err
	}
	err = data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		q := c.Product.Update().Where(product.ID(req.Id), product.SubsiteID(tenancy.FromContext(ctx).SubsiteID), product.StatusGTE(0), product.LockVersion(req.ExpectedVersion)).SetIsLocked(req.IsLocked).AddLockVersion(1).SetListingChangedAt(time.Now().UnixMilli()).SetListingZeroSince(0)
		if req.IsLocked {
			q.SetLockedBy(actor).SetLockedAt(time.Now().UTC())
		} else {
			q.SetLockedBy(0).ClearLockedAt()
		}
		n, e := q.Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return errors.Conflict("catalog.LOCK_STALE", "商品状态已变化，请刷新后重试")
		}
		p, e := c.Product.Get(ctx, req.Id)
		if e != nil {
			return e
		}
		out = ToAdminPB(p)
		return c.AuditLog.Create().SetOperatorType("admin").SetOperatorID(actor).SetPermissionPoint("catalog:lock").SetAction("PUT").SetRoute("/api/v1/admin/products/{id}/lock").SetAfter(map[string]any{"product_id": req.Id, "is_locked": req.IsLocked}).Exec(ctx)
	})
	return
}

// A batch ignores only locked rows. Missing/foreign/deleted IDs still fail;
// they must never be silently counted as successful updates.
func editableBatch(ctx context.Context, d *data.Data, ids []uint64) ([]*ent.Product, int32, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 || len(ids) > 1000 || ids[0] == 0 {
		return nil, 0, errors.BadRequest("catalog.BATCH_INVALID", "请选择 1～1000 件商品")
	}
	rows := make([]*ent.Product, 0, len(ids))
	var skipped int32
	for _, id := range ids {
		p, err := data.GuardProductWrite(ctx, d, id)
		if data.IsProductLocked(err) {
			skipped++
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		rows = append(rows, p)
	}
	return rows, skipped, nil
}

func (r *ProductRepoImpl) batchStatus(ctx context.Context, ids []uint64, status int8) (updated int, skipped int32, err error) {
	if status < 0 || status > 2 {
		return 0, 0, errors.BadRequest("catalog.STATUS_INVALID", "请选择有效的上下架状态")
	}
	err = data.Tx(ctx, r.data, func(ctx context.Context) error {
		rows, n, e := editableBatch(ctx, r.data, ids)
		if e != nil {
			return e
		}
		skipped = n
		for _, p := range rows {
			if e = data.ManualListing(data.Client(ctx, r.data).Product.UpdateOneID(p.ID), status).Exec(ctx); e != nil {
				return e
			}
			updated++
		}
		return nil
	})
	return
}

func productLockedAt(p *ent.Product) int64 {
	if p.LockedAt != nil {
		return p.LockedAt.Unix()
	}
	return 0
}
