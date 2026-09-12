package catalog

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
)

// BatchUpdateProductCategory 全部成功或全部回滚，只修改本站未删除商品的分类。
func (s *AdminCatalogService) BatchUpdateProductCategory(ctx context.Context, req *adminv1.BatchUpdateProductCategoryRequest) (*adminv1.BatchUpdateProductCategoryReply, error) {
	if len(req.GetIds()) == 0 || len(req.GetIds()) > 1000 || req.GetCategoryId() == 0 {
		return nil, errors.BadRequest("catalog.BATCH_CATEGORY_INVALID", "请选择 1～1000 件商品和目标分类")
	}
	ids := make([]uint64, 0, len(req.Ids))
	seen := make(map[uint64]bool)
	for _, id := range req.Ids {
		if id == 0 {
			return nil, errors.BadRequest("catalog.BATCH_CATEGORY_INVALID", "商品编号无效，请刷新后重试")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		subsite := tenancy.FromContext(ctx).SubsiteID
		// 条件写锁定分类，避免校验后被删除；不改变分类排序或可见性。
		n, err := c.Category.Update().Where(category.ID(req.CategoryId), category.SubsiteID(subsite)).AddSort(0).Save(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.BadRequest("catalog.CATEGORY_NOT_FOUND", "目标分类不存在，请刷新分类后重试")
		}
		n, err = c.Product.Update().Where(product.IDIn(ids...), product.SubsiteID(subsite), product.StatusGTE(0)).SetCategoryID(req.CategoryId).Save(ctx)
		if err != nil {
			return err
		}
		if n != len(ids) {
			return errors.BadRequest("catalog.BATCH_CATEGORY_STALE", "部分商品不存在或已删除，本次未修改任何商品，请刷新列表后重试")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &adminv1.BatchUpdateProductCategoryReply{Updated: int32(len(ids))}, nil
}
