package catalog

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	placement "github.com/NovaWorks/zcard-next/server/internal/data/ent/categoryproductplacement"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
	"slices"
)

// Category structure changes and placement saves use the same ordered row locks.
// This prevents moving/deleting a category between membership validation and save.
func lockCategoryStructure(ctx context.Context, d *data.Data) error {
	c := data.Client(ctx, d)
	ids, err := c.Category.Query().Where(category.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).Order(ent.Asc(category.FieldID)).IDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = c.Category.UpdateOneID(id).AddPlacementVersion(0).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *AdminCatalogService) GetCategoryPlacements(ctx context.Context, req *adminv1.GetCategoryPlacementsRequest) (*adminv1.CategoryPlacementsReply, error) {
	c := data.Client(ctx, s.repo.data)
	tenant := tenancy.FromContext(ctx).SubsiteID
	cat, err := c.Category.Query().Where(category.ID(req.CategoryId), category.SubsiteID(tenant)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("catalog.CATEGORY_NOT_FOUND", "分类不存在")
	}
	if err != nil {
		return nil, err
	}
	rows, err := c.CategoryProductPlacement.Query().Where(placement.SubsiteID(tenant), placement.CategoryID(cat.ID)).Order(ent.Desc(placement.FieldIsPinned), ent.Asc(placement.FieldPosition), ent.Asc(placement.FieldProductID)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := &adminv1.CategoryPlacementsReply{CategoryId: cat.ID, Version: cat.PlacementVersion, Items: []*adminv1.CategoryPlacementItem{}}
	ids := make([]uint64, 0, len(rows))
	for _, p := range rows {
		ids = append(ids, p.ProductID)
	}
	products, err := c.Product.Query().Where(product.SubsiteID(tenant), product.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[uint64]*ent.Product{}
	for _, p := range products {
		byID[p.ID] = p
	}
	descendants, err := descendantCategoryIDs(ctx, c, cat.ID)
	if err != nil {
		return nil, err
	}
	hidden, err := data.HiddenCategoryIDs(ctx, c, tenant)
	if err != nil {
		return nil, err
	}
	for _, p := range rows {
		item := &adminv1.CategoryPlacementItem{Placement: &adminv1.CategoryPlacementInput{ProductId: p.ProductID, IsPinned: p.IsPinned, IsRecommended: p.IsRecommended, Position: p.Position}}
		row := byID[p.ProductID]
		switch {
		case row == nil || row.Status < 0:
			item.UnavailableReason = "商品已删除，设置已失效"
		case !descendants[row.CategoryID]:
			item.UnavailableReason = "商品已移出本分类范围，设置已失效"
		case data.CategoryHidden(hidden, row.CategoryID):
			item.UnavailableReason = "商品所属分类或上级分类已隐藏"
		case row.Status != 1:
			item.UnavailableReason = "商品未上架，恢复上架后展示"
		}
		if row != nil {
			item.Product = ToAdminPB(row)
		} else {
			item.Product = &adminv1.AdminProduct{Id: p.ProductID, Name: "已删除商品"}
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (s *AdminCatalogService) SetCategoryPlacements(ctx context.Context, req *adminv1.SetCategoryPlacementsRequest) (out *adminv1.CategoryPlacementsReply, err error) {
	actor, err := batchActor(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Items) > 1000 {
		return nil, errors.BadRequest("catalog.PLACEMENT_LIMIT", "最多配置 1000 件商品")
	}
	wanted := map[uint64]*adminv1.CategoryPlacementInput{}
	positions := map[int32]bool{}
	for _, p := range req.Items {
		if p == nil || p.ProductId == 0 || wanted[p.ProductId] != nil || p.Position < 0 || (!p.IsPinned && !p.IsRecommended) {
			return nil, errors.BadRequest("catalog.PLACEMENT_INVALID", "商品重复或设置无效，请重新选择")
		}
		if p.IsPinned && positions[p.Position] {
			return nil, errors.BadRequest("catalog.PLACEMENT_INVALID", "置顶顺序不能重复，请重新调整")
		}
		if p.IsPinned {
			positions[p.Position] = true
		}
		wanted[p.ProductId] = p
	}
	err = data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		tenant := tenancy.FromContext(ctx).SubsiteID
		if e := lockCategoryStructure(ctx, s.repo.data); e != nil {
			return e
		}
		n, e := c.Category.Update().Where(category.ID(req.CategoryId), category.SubsiteID(tenant), category.PlacementVersion(req.ExpectedVersion)).AddPlacementVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return errors.Conflict("catalog.PLACEMENT_STALE", "分类设置已被更新，请重新加载后调整；当前草稿已保留")
		}
		current, e := c.CategoryProductPlacement.Query().Where(placement.SubsiteID(tenant), placement.CategoryID(req.CategoryId)).All(ctx)
		if e != nil {
			return e
		}
		old := map[uint64]*ent.CategoryProductPlacement{}
		ids := []uint64{}
		for _, p := range current {
			old[p.ProductID] = p
			ids = append(ids, p.ProductID)
		}
		for id := range wanted {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		ids = slices.Compact(ids)
		descendants, e := descendantCategoryIDs(ctx, c, req.CategoryId)
		if e != nil {
			return e
		}
		for _, id := range ids {
			before, next := old[id], wanted[id]
			if before != nil && next != nil && before.IsPinned == next.IsPinned && before.IsRecommended == next.IsRecommended && before.Position == next.Position {
				continue
			}
			p, e := c.Product.Query().Where(product.ID(id), product.SubsiteID(tenant)).Only(ctx)
			// Removed archived products may be cleaned from category configuration.
			if (ent.IsNotFound(e) || (e == nil && p.Status < 0)) && next == nil {
				if before != nil {
					if e = c.CategoryProductPlacement.DeleteOneID(before.ID).Exec(ctx); e != nil {
						return e
					}
				}
				continue
			}
			if e != nil {
				return errors.BadRequest("catalog.PLACEMENT_PRODUCT_MISSING", "商品不存在或不属于当前站点")
			}
			p, e = data.GuardProductWrite(ctx, s.repo.data, id)
			if e != nil {
				return e
			}
			if next != nil && !descendants[p.CategoryID] {
				return errors.BadRequest("catalog.PLACEMENT_OUTSIDE", "商品已不属于当前分类或其子分类，请重新选择")
			}
			if next == nil {
				if e = c.CategoryProductPlacement.DeleteOneID(before.ID).Exec(ctx); e != nil {
					return e
				}
				continue
			}
			if before == nil {
				e = c.CategoryProductPlacement.Create().SetSubsiteID(tenant).SetCategoryID(req.CategoryId).SetProductID(id).SetIsPinned(next.IsPinned).SetIsRecommended(next.IsRecommended).SetPosition(next.Position).Exec(ctx)
			} else {
				e = c.CategoryProductPlacement.UpdateOneID(before.ID).SetIsPinned(next.IsPinned).SetIsRecommended(next.IsRecommended).SetPosition(next.Position).Exec(ctx)
			}
			if e != nil {
				return e
			}
		}
		if e = c.AuditLog.Create().SetOperatorType("admin").SetOperatorID(actor).SetPermissionPoint("catalog:category_write").SetAction("PUT").SetRoute("/api/v1/admin/categories/{category_id}/placements").SetAfter(map[string]any{"category_id": req.CategoryId, "items": req.Items}).Exec(ctx); e != nil {
			return e
		}
		out, e = s.GetCategoryPlacements(ctx, &adminv1.GetCategoryPlacementsRequest{CategoryId: req.CategoryId})
		return e
	})
	return
}
