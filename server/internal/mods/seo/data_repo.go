package seo

// 数据查询：sitemap 数据源（上架商品/已发布文章/商品分类）。

import (
	"context"
	"encoding/json"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/post"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
)

// SitemapProduct 商品 sitemap 条目。
type SitemapProduct struct {
	ID        uint64
	UpdatedAt int64
}

// SitemapPost 文章 sitemap 条目。
type SitemapPost struct {
	Slug        string
	PublishedAt int64
}

// ProductSEO 商品动态渲染数据（爬虫视角）。
type ProductOfferSEO struct {
	SKU        uint64
	PriceCents int64
	Stock      int64
}

type ProductSEO struct {
	Offers          []ProductOfferSEO
	ID              uint64
	Name            string
	DescriptionHTML string // 入库前已 sanitize
	Cover           string
	Stock           int64 // >=0 finite, -1 unlimited, -2 unknown; shared with storefront
	PriceCents      int64
}

// PostSEO 文章动态渲染数据（爬虫视角；多语言已回落到单值）。
type PostSEO struct {
	Slug        string
	Title       string
	Summary     string
	Thumbnail   string
	ContentHTML string // 入库前已 sanitize
	PublishedAt int64
}

// GetPostSEO 按 slug 取已发布文章（未发布/不存在 → nil；多语言 zh_CN → zh → 首个非空）。
func (r *SeoRepo) GetPostSEO(ctx context.Context, slug string) (*PostSEO, error) {
	p, err := data.Client(ctx, r.data).Post.Query().
		Where(post.Slug(slug), post.IsPublished(true), post.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	var content map[string]string
	_ = json.Unmarshal([]byte(p.ContentJSON), &content)
	out := &PostSEO{
		Slug:        p.Slug,
		Thumbnail:   p.Thumbnail,
		Title:       langValue(p.TitleJSON),
		Summary:     langValue(p.SummaryJSON),
		ContentHTML: langValue(content),
	}
	if !p.PublishedAt.IsZero() {
		out.PublishedAt = p.PublishedAt.Unix()
	}
	return out, nil
}

// langValue 多语言回落：zh_CN → zh → 首个非空值。
func langValue(m map[string]string) string {
	if v := m["zh_CN"]; v != "" {
		return v
	}
	if v := m["zh"]; v != "" {
		return v
	}
	for _, v := range m {
		if v != "" {
			return v
		}
	}
	return ""
}

// SeoRepo sitemap 数据仓储。
type SeoRepo struct {
	data *data.Data
}

// NewSeoRepo 构造。
func NewSeoRepo(d *data.Data) *SeoRepo {
	return &SeoRepo{data: d}
}

// ListSitemapProducts 上架商品（status=1；隐藏商品不进收录）。
func (r *SeoRepo) ListSitemapProducts(ctx context.Context) ([]SitemapProduct, error) {
	hidden, err := data.HiddenCategoryIDs(ctx, data.Client(ctx, r.data), tenancy.FromContext(ctx).SubsiteID)
	if err != nil {
		return nil, err
	}
	rows, err := data.Client(ctx, r.data).Product.Query().Where(data.VisibleProductCategory(hidden)).
		Where(product.Status(1), product.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).
		Select(product.FieldID, product.FieldUpdatedAt).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SitemapProduct, 0, len(rows))
	for _, p := range rows {
		out = append(out, SitemapProduct{ID: p.ID, UpdatedAt: p.UpdatedAt.Unix()})
	}
	return out, nil
}

// ListSitemapPosts 已发布文章。
func (r *SeoRepo) ListSitemapPosts(ctx context.Context) ([]SitemapPost, error) {
	rows, err := data.Client(ctx, r.data).Post.Query().
		Where(post.IsPublished(true), post.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).
		Select(post.FieldSlug, post.FieldPublishedAt).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SitemapPost, 0, len(rows))
	for _, p := range rows {
		ts := int64(0)
		if !p.PublishedAt.IsZero() {
			ts = p.PublishedAt.Unix()
		}
		out = append(out, SitemapPost{Slug: p.Slug, PublishedAt: ts})
	}
	return out, nil
}

// ListPostSEO reads only public post summaries; delivery and account data never enter the fallback.
func (r *SeoRepo) ListPostSEO(ctx context.Context, typ string, categoryID uint64, page, size int) ([]PostSEO, int64, error) {
	q := data.Client(ctx, r.data).Post.Query().Where(post.IsPublished(true), post.SubsiteID(tenancy.FromContext(ctx).SubsiteID))
	if typ != "" {
		q = q.Where(post.TypeEQ(post.Type(typ)))
	}
	if categoryID > 0 {
		q = q.Where(post.CategoryID(categoryID))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(ent.Asc(post.FieldSort), ent.Desc(post.FieldPublishedAt), ent.Desc(post.FieldID)).Offset((page - 1) * size).Limit(size).All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]PostSEO, 0, len(rows))
	for _, p := range rows {
		out = append(out, PostSEO{Slug: p.Slug, Title: langValue(p.TitleJSON)})
	}
	return out, int64(total), nil
}
