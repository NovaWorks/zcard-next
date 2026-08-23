package seo

// 数据查询：sitemap 数据源（上架商品/已发布文章/商品分类）。

import (
	"context"
	"encoding/json"

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
type ProductSEO struct {
	ID              uint64
	Name            string
	DescriptionHTML string // 入库前已 sanitize
	Cover           string
	Images          []string
	PriceCents      int64
	UpdatedAt       int64
}

// PostSEO 文章动态渲染数据（爬虫视角；多语言已回落到单值）。
type PostSEO struct {
	Slug         string
	Title        string
	Summary      string
	ContentHTML  string // 入库前已 sanitize
	PublishedAt  int64
}

// GetProductSEO 按 id 取上架商品（status=1；未上架/不存在 → nil）。
func (r *SeoRepo) GetProductSEO(ctx context.Context, id uint64) (*ProductSEO, error) {
	p, err := data.Client(ctx, r.data).Product.Query().
		Where(product.ID(id), product.Status(1)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &ProductSEO{
		ID:              p.ID,
		Name:            p.Name,
		DescriptionHTML: p.Description,
		Cover:           p.Cover,
		Images:          p.Images,
		PriceCents:      p.Price,
		UpdatedAt:       p.UpdatedAt.Unix(),
	}, nil
}

// GetPostSEO 按 slug 取已发布文章（未发布/不存在 → nil；多语言 zh_CN → zh → 首个非空）。
func (r *SeoRepo) GetPostSEO(ctx context.Context, slug string) (*PostSEO, error) {
	p, err := data.Client(ctx, r.data).Post.Query().
		Where(post.Slug(slug), post.IsPublished(true)).
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
	rows, err := data.Client(ctx, r.data).Product.Query().
		Where(product.Status(1)).
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
		Where(post.IsPublished(true)).
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
