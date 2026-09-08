package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

func (s *AdminCatalogService) MergeCategories(ctx context.Context, req *adminv1.MergeCategoriesRequest) (*adminv1.MergeCategoriesReply, error) {
	return s.repo.mergeCategories(ctx, req)
}

// 预览和提交共用校验；商品、子分类、渠道配置与映射在同一事务内迁移。
func (r *ProductRepoImpl) mergeCategories(ctx context.Context, req *adminv1.MergeCategoriesRequest) (*adminv1.MergeCategoriesReply, error) {
	reply := &adminv1.MergeCategoriesReply{}
	err := data.Tx(ctx, r.data, func(ctx context.Context) error {
		c := data.Client(ctx, r.data)
		sources := map[uint64]bool{}
		for _, id := range req.SourceIds {
			if id > 0 {
				sources[id] = true
			}
		}
		if len(sources) == 0 || req.TargetId == 0 || sources[req.TargetId] {
			return fmt.Errorf("请选择来源分类和不同的目标分类")
		}
		ids := make([]uint64, 0, len(sources))
		for id := range sources {
			ids = append(ids, id)
		}
		tenant := tenancy.FromContext(ctx).SubsiteID
		cats, err := c.Category.Query().Where(category.SubsiteID(tenant)).All(ctx)
		if err != nil {
			return err
		}
		parents := map[uint64]uint64{}
		for _, cat := range cats {
			parents[cat.ID] = cat.ParentID
		}
		if _, ok := parents[req.TargetId]; !ok {
			return fmt.Errorf("目标分类不存在")
		}
		for id := range sources {
			if _, ok := parents[id]; !ok {
				return fmt.Errorf("来源分类已变化，请刷新后重试")
			}
		}
		seen := map[uint64]bool{}
		for id := req.TargetId; id != 0; id = parents[id] {
			if sources[id] || seen[id] {
				return fmt.Errorf("不能合并到来源分类的子分类中")
			}
			seen[id] = true
		}
		// 分类优惠直接改成目标会扩大优惠范围，因此要求先由运营调整活动。
		referenced := func(scope map[string]any) bool {
			raw, _ := json.Marshal(scope["category_ids"])
			var values []any
			_ = json.Unmarshal(raw, &values)
			for _, v := range values {
				id, _ := strconv.ParseUint(fmt.Sprint(v), 10, 64)
				if sources[id] {
					return true
				}
			}
			return false
		}
		coupons, err := c.Coupon.Query().All(ctx)
		if err != nil {
			return err
		}
		for _, row := range coupons {
			if referenced(row.Scope) {
				return fmt.Errorf("来源分类被优惠券引用，请先调整优惠券适用范围")
			}
		}
		promotions, err := c.Promotion.Query().All(ctx)
		if err != nil {
			return err
		}
		for _, row := range promotions {
			if referenced(row.Scope) {
				return fmt.Errorf("来源分类被促销活动引用，请先调整活动适用范围")
			}
		}
		products, err := c.Product.Query().Where(product.CategoryIDIn(ids...), product.SubsiteID(tenant)).Count(ctx)
		if err != nil {
			return err
		}
		children, err := c.Category.Query().Where(category.ParentIDIn(ids...), category.IDNotIn(ids...), category.SubsiteID(tenant)).Count(ctx)
		if err != nil {
			return err
		}
		reply.Categories = int32(len(ids))
		reply.Products = int32(products)
		reply.Children = int32(children)
		if req.Preview {
			return nil
		}
		conns, err := c.SupplyConnection.Query().Order(ent.Asc(supplyconnection.FieldID)).All(ctx)
		if err != nil {
			return err
		}
		for _, conn := range conns {
			// 获取最新配置后修改，避免覆盖其他渠道设置。
			if err := c.SupplyConnection.UpdateOneID(conn.ID).AddRetryMax(0).Exec(ctx); err != nil {
				return err
			}
			fresh, err := c.SupplyConnection.Get(ctx, conn.ID)
			if err != nil {
				return err
			}
			mapping, _ := fresh.Settings["category_map"].(map[string]any)
			changed := false
			for code, v := range mapping {
				id, _ := strconv.ParseUint(fmt.Sprint(v), 10, 64)
				if sources[id] {
					mapping[code] = req.TargetId
					changed = true
				}
			}
			if changed {
				if err := c.SupplyConnection.UpdateOneID(conn.ID).SetSettings(fresh.Settings).Exec(ctx); err != nil {
					return err
				}
			}
		}
		if _, err = c.Product.Update().Where(product.CategoryIDIn(ids...), product.SubsiteID(tenant)).SetCategoryID(req.TargetId).Save(ctx); err != nil {
			return err
		}
		if _, err = c.Category.Update().Where(category.ParentIDIn(ids...), category.IDNotIn(ids...), category.SubsiteID(tenant)).SetParentID(req.TargetId).Save(ctx); err != nil {
			return err
		}
		if _, err = c.SupplyMapping.Update().Where(supplymapping.LocalCategoryIDIn(ids...)).SetLocalCategoryID(req.TargetId).Save(ctx); err != nil {
			return err
		}
		// 先解除来源之间父子引用，再统一删除；未选中的子分类已迁移。
		if _, err = c.Category.Update().Where(category.IDIn(ids...)).ClearParentID().Save(ctx); err != nil {
			return err
		}
		_, err = c.Category.Delete().Where(category.IDIn(ids...), category.SubsiteID(tenant)).Exec(ctx)
		return err
	})
	return reply, err
}
