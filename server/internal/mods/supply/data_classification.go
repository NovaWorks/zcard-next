package supply

import (
	"context"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"strings"
	"unicode/utf8"
)

func categoryRuleMatch(name string, rule *adminv1.ProductCategoryRule) bool {
	if rule == nil {
		return false
	}
	return data.CategoryKeywordMatch(name, rule.Keywords, rule.Excludes, rule.MatchAll)
}

func (s *AdminSupplyService) resolveImportCategories(ctx context.Context, req *adminv1.ImportProductsRequest, products map[string]adapter.Product, groups map[string]uint64) (map[string]uint64, error) {
	if len(req.CategoryRules) > 100 {
		return nil, fmt.Errorf("分类规则最多 100 条")
	}
	selected := map[string]bool{}
	result := map[string]uint64{}
	targets := map[uint64]bool{}
	for _, code := range req.Codes {
		selected[code] = true
	}
	for _, rule := range req.CategoryRules {
		if rule == nil || rule.CategoryId == 0 || len(rule.Keywords) == 0 || len(rule.Keywords)+len(rule.Excludes) > 100 {
			return nil, fmt.Errorf("请填写分类规则的关键词和目标分类")
		}
		for _, word := range append(append([]string{}, rule.Keywords...), rule.Excludes...) {
			if strings.TrimSpace(word) == "" || utf8.RuneCountInString(word) > 100 {
				return nil, fmt.Errorf("分类关键词须为 1–100 个字符")
			}
		}
		targets[rule.CategoryId] = true
	}
	for code, id := range req.ProductCategories {
		if !selected[code] {
			return nil, fmt.Errorf("只能指定本次选中商品的分类")
		}
		result[code] = id
		targets[id] = true
	}
	locals, err := s.repo.entClient(ctx).Product.Query().Where(product.SubsiteID(tenancy.FromContext(ctx).SubsiteID), product.UpstreamSourceID(req.ConnectionId), product.UpstreamProductCodeIn(req.Codes...), product.CategoryProtected(true), product.StatusGTE(0)).All(ctx)
	if err != nil {
		return nil, err
	}
	protected := map[string]bool{}
	for _, p := range locals {
		protected[p.UpstreamProductCode] = true
	}
	for _, code := range req.Codes {
		// Only an explicit per-product override can replace a protected category.
		if protected[code] {
			continue
		}
		if _, ok := result[code]; ok {
			continue
		}
		for _, rule := range req.CategoryRules {
			if categoryRuleMatch(products[code].Name, rule) {
				result[code] = rule.CategoryId
				break
			}
		}
		if _, ok := result[code]; !ok && req.SelectedCategoriesOnly {
			group := products[code].CategoryID
			_, explicit := req.CategoryMap[group]
			for _, draft := range req.CategoryDrafts {
				if draft.UpstreamCode == group {
					explicit = true
				}
			}
			if explicit {
				result[code] = groups[group]
			}
		}
	}
	for id := range targets {
		if id == 0 {
			continue
		}
		ok, err := s.repo.entClient(ctx).Category.Query().Where(category.ID(id), category.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).Exist(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("分类规则的目标分类已不存在，请重新选择")
		}
	}
	return result, nil
}
