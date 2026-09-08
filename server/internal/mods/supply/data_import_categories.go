package supply

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// 保存前先校验选择范围；事务只包含分类和配置写入，不包含上游请求或图片下载。
func (s *AdminSupplyService) saveImportCategories(ctx context.Context, req *adminv1.ImportProductsRequest, products map[string]adapter.Product, mode string, percent float64, amount int64) (map[string]uint64, error) {
	selected := map[string]bool{}
	for _, code := range req.Codes {
		p, ok := products[code]
		if !ok {
			return nil, fmt.Errorf("商品 %s 已不可用，请刷新目录后重试", code)
		}
		selected[p.CategoryID] = true
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("请先选择商品")
	}
	for code := range req.CategoryMap {
		if !selected[code] {
			return nil, fmt.Errorf("只能保存所选商品涉及的分类映射")
		}
	}
	drafts := map[string]bool{}
	for _, draft := range req.CategoryDrafts {
		if draft == nil || !selected[draft.UpstreamCode] {
			return nil, fmt.Errorf("待新建分类必须对应所选商品")
		}
		if drafts[draft.UpstreamCode] {
			return nil, fmt.Errorf("重复的分类草稿")
		}
		drafts[draft.UpstreamCode] = true
		if _, ok := req.CategoryMap[draft.UpstreamCode]; ok {
			return nil, fmt.Errorf("同一分类不能同时新建和指定映射")
		}
		name := strings.TrimSpace(draft.Name)
		if name == "" || utf8.RuneCountInString(name) > 100 {
			return nil, fmt.Errorf("分类名称须为 1–100 个字符")
		}
	}
	result := map[string]uint64{}
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		client := s.repo.entClient(ctx)
		// 写锁串行化同一渠道的提交；重试读取最新映射，避免重复创建。
		if err := client.SupplyConnection.UpdateOneID(req.ConnectionId).AddRetryMax(0).Exec(ctx); err != nil {
			return err
		}
		conn, err := client.SupplyConnection.Get(ctx, req.ConnectionId)
		if err != nil {
			return err
		}
		settings := conn.Settings
		if settings == nil {
			settings = map[string]any{}
		}
		for k, v := range categoryMapFromSettings(settings) {
			result[k] = v
		}
		mappings, err := client.SupplyMapping.Query().Where(supplymapping.ConnectionID(conn.ID)).All(ctx)
		if err != nil {
			return err
		}
		for _, m := range mappings {
			if _, ok := result[m.UpstreamCategory]; !ok && m.LocalCategoryID > 0 {
				result[m.UpstreamCategory] = m.LocalCategoryID
			}
		}
		tenant := tenancy.FromContext(ctx).SubsiteID
		validate := func(id uint64) error {
			if id == 0 {
				return nil
			}
			exists, err := client.Category.Query().Where(category.ID(id), category.SubsiteID(tenant)).Exist(ctx)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("所选本地分类或父分类已不存在，请刷新后重试")
			}
			return nil
		}
		for code, id := range req.CategoryMap {
			if err := validate(id); err != nil {
				return err
			}
			result[code] = id // 0 是显式清除，不能回退旧映射。
		}
		for _, draft := range req.CategoryDrafts {
			if err := validate(draft.ParentId); err != nil {
				return err
			}
			name := strings.TrimSpace(draft.Name)
			q := client.Category.Query().Where(category.SubsiteID(tenant), category.Name(name))
			if draft.ParentId == 0 {
				q.Where(category.Or(category.ParentIDIsNil(), category.ParentID(0)))
			} else {
				q.Where(category.ParentID(draft.ParentId))
			}
			matches, err := q.All(ctx)
			if err != nil {
				return err
			}
			if len(matches) > 1 {
				return fmt.Errorf("同一位置有多个同名分类「%s」，请手动选择", name)
			}
			if len(matches) == 1 {
				result[draft.UpstreamCode] = matches[0].ID
				continue
			}
			create := client.Category.Create().SetSubsiteID(tenant).SetName(name)
			if draft.ParentId > 0 {
				create.SetParentID(draft.ParentId)
			}
			cat, err := create.Save(ctx)
			if err != nil {
				return err
			}
			result[draft.UpstreamCode] = cat.ID
		}
		for code := range selected {
			if err := validate(result[code]); err != nil {
				return err
			}
		}
		merged := map[string]any{}
		for code, id := range result {
			merged[code] = id
		}
		settings["category_map"] = merged
		if req.SaveDefault {
			settings["import_pricing"] = map[string]any{"mode": mode, "markup_percent": percent, "markup_amount_cents": amount}
		}
		return client.SupplyConnection.UpdateOneID(conn.ID).SetSettings(settings).Exec(ctx)
	})
	return result, err
}
