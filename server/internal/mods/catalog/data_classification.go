package catalog

import (
	"context"
	"crypto/sha256"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"sort"
	"strings"
	"unicode/utf8"
)

func classificationRevision(p *ent.Product) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%t:%s", p.CategoryID, p.LockVersion, p.CategoryProtected, p.Name))))
}
func (s *AdminCatalogService) ClassifyProducts(ctx context.Context, req *adminv1.ClassifyProductsRequest) (*adminv1.ClassifyProductsReply, error) {
	if len(req.Keywords) == 0 || len(req.Keywords)+len(req.Excludes) > 100 || req.CategoryId == 0 || len(req.Revisions) > 1000 {
		return nil, fmt.Errorf("请输入关键词和目标分类，每次最多处理1000件商品")
	}
	for _, w := range append(append([]string{}, req.Keywords...), req.Excludes...) {
		if strings.TrimSpace(w) == "" || utf8.RuneCountInString(w) > 100 {
			return nil, fmt.Errorf("关键词须为1–100个字符")
		}
	}
	c := data.Client(ctx, s.repo.data)
	tenant := tenancy.FromContext(ctx).SubsiteID
	ok, e := c.Category.Query().Where(category.ID(req.CategoryId), category.SubsiteID(tenant)).Exist(ctx)
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, fmt.Errorf("目标分类不存在")
	}
	out := &adminv1.ClassifyProductsReply{}
	if !req.Apply {
		var cursor uint64
		for {
			rows, e := c.Product.Query().Where(product.SubsiteID(tenant), product.StatusGTE(0), product.IDGT(cursor)).Order(ent.Asc(product.FieldID)).Limit(500).All(ctx)
			if e != nil {
				return nil, e
			}
			for _, p := range rows {
				cursor = p.ID
				if !data.CategoryKeywordMatch(p.Name, req.Keywords, req.Excludes, req.MatchAll) {
					continue
				}
				if p.IsLocked {
					out.SkippedLocked++
					continue
				}
				if p.CategoryID == req.CategoryId {
					out.Unchanged++
					continue
				}
				if p.CategoryProtected {
					out.SkippedProtected++
					continue
				}
				if len(out.Items) == 1000 {
					out.Truncated = true
					return out, nil
				}
				out.Items = append(out.Items, &adminv1.ClassificationProduct{Id: p.ID, Name: p.Name, CategoryId: p.CategoryID, Revision: classificationRevision(p), Status: "matched"})
			}
			if len(rows) < 500 {
				break
			}
		}
		return out, nil
	}
	if len(req.Revisions) == 0 {
		return nil, fmt.Errorf("请先预览将要修改的商品")
	}
	ids := make([]uint64, 0, len(req.Revisions))
	for id := range req.Revisions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	e = data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		n, e := c.Category.Update().Where(category.ID(req.CategoryId), category.SubsiteID(tenant)).AddSort(0).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return fmt.Errorf("目标分类已不存在，请重新预览")
		}
		for _, id := range ids {
			p, e := data.GuardProductWrite(ctx, s.repo.data, id)
			if data.IsProductLocked(e) {
				out.SkippedLocked++
				continue
			}
			if e != nil {
				return e
			}
			row := &adminv1.ClassificationProduct{Id: id, Name: p.Name, CategoryId: p.CategoryID, Revision: classificationRevision(p)}
			if row.Revision != req.Revisions[id] || !data.CategoryKeywordMatch(p.Name, req.Keywords, req.Excludes, req.MatchAll) {
				row.Status = "conflict"
			} else if p.CategoryProtected {
				row.Status = "protected"
				out.SkippedProtected++
			} else {
				if e := c.Product.UpdateOneID(id).SetCategoryID(req.CategoryId).SetCategoryProtected(true).Exec(ctx); e != nil {
					return e
				}
				row.Status = "updated"
				out.Updated++
			}
			out.Items = append(out.Items, row)
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
