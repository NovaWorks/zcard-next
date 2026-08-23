package seo

// 数据查询：sitemap 数据源（上架商品/已发布文章/商品分类）。

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data"
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
