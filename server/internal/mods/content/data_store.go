package content

// 前台 API（）：生效横幅、已发布文章、分类。
// 多语言按 locale 回落（zh_CN 默认）；移动端图缺省回落 PC 图。
// 30s 进程内缓存（低频内容；有 Redis 的部署由网关/CDN 层覆盖，不在进程内重复建）。

import (
	"context"
	"sync"
	"time"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/i18n"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// StoreContentService 前台内容服务。
type StoreContentService struct {
	storefrontv1.UnimplementedStoreContentServiceServer
	repo *ContentRepo

	// 30s 进程内缓存（ListBanners 热路径；发布新文章走分页查询不缓存）
	cacheMu   sync.Mutex
	cacheAt   time.Time
	cacheKey  bannerCacheKey
	cacheData []*storefrontv1.StoreBanner
}

type bannerCacheKey struct {
	tenant   uint64
	position string
	locale   i18n.Locale
}

// NewStoreContentService 构造。
func NewStoreContentService(repo *ContentRepo) *StoreContentService {
	return &StoreContentService{repo: repo}
}

// bannerCacheTTL 横幅缓存时长（测试可注入缩短）。
var bannerCacheTTL = 30 * time.Second

// ListBanners 生效中横幅（position + 时间窗 + sort）。
func (s *StoreContentService) ListBanners(ctx context.Context, req *storefrontv1.ListBannersRequest) (*storefrontv1.ListBannersReply, error) {
	locale := i18n.ResolveContext(ctx, req.GetLocale())
	key := bannerCacheKey{tenant: tenancy.FromContext(ctx).SubsiteID, position: req.GetPosition(), locale: locale}

	// Cache entries include both tenant and language, even when the position matches.
	s.cacheMu.Lock()
	if s.cacheKey == key && time.Since(s.cacheAt) < bannerCacheTTL && s.cacheData != nil {
		cached := s.cacheData
		s.cacheMu.Unlock()
		return &storefrontv1.ListBannersReply{Banners: cached}, nil
	}
	s.cacheMu.Unlock()

	rows, err := s.repo.ListActiveBanners(ctx, key.position)
	if err != nil {
		return nil, err
	}
	out := make([]*storefrontv1.StoreBanner, 0, len(rows))
	for _, b := range rows {
		mobile := b.MobileImage
		if mobile == "" {
			mobile = b.Image // 移动端缺省回落 PC 图
		}
		out = append(out, &storefrontv1.StoreBanner{
			Id:          b.ID,
			Title:       LangValue(b.TitleJSON, string(locale)),
			Image:       b.Image,
			MobileImage: mobile,
			LinkType:    string(b.LinkType),
			LinkValue:   b.LinkValue,
		})
	}

	s.cacheMu.Lock()
	s.cacheKey, s.cacheAt, s.cacheData = key, time.Now(), out
	s.cacheMu.Unlock()
	return &storefrontv1.ListBannersReply{Banners: out}, nil
}

// ListPosts 已发布文章分页（type + 栏目过滤）。
func (s *StoreContentService) ListPosts(ctx context.Context, req *storefrontv1.ListPostsRequest) (*storefrontv1.ListPostsReply, error) {
	locale := string(i18n.ResolveContext(ctx, req.GetLocale()))
	page, size := pageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListPublishedPosts(ctx, req.GetType(), req.GetCategoryId(), page, size)
	if err != nil {
		return nil, err
	}
	reply := &storefrontv1.ListPostsReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, p := range rows {
		reply.Posts = append(reply.Posts, &storefrontv1.StorePost{
			Id: p.ID, Slug: p.Slug, Type: string(p.Type),
			Title: LangValue(p.TitleJSON, locale), Thumbnail: p.Thumbnail,
			Summary:    LangValue(p.SummaryJSON, locale),
			CategoryId: p.CategoryID,
			PublishedAt: func() int64 {
				if p.PublishedAt.IsZero() {
					return 0
				}
				return p.PublishedAt.Unix()
			}(),
		})
	}
	return reply, nil
}

// GetPost 文章详情（slug；未发布 404 语义）。
func (s *StoreContentService) GetPost(ctx context.Context, req *storefrontv1.GetPostRequest) (*storefrontv1.GetPostReply, error) {
	locale := string(i18n.ResolveContext(ctx, req.GetLocale()))
	p, err := s.repo.GetPublishedBySlug(ctx, req.GetSlug())
	if err != nil {
		return nil, err
	}
	return &storefrontv1.GetPostReply{
		Post: &storefrontv1.StorePost{
			Id: p.ID, Slug: p.Slug, Type: string(p.Type),
			Title: LangValue(p.TitleJSON, locale), Thumbnail: p.Thumbnail,
			Summary:    LangValue(p.SummaryJSON, locale),
			CategoryId: p.CategoryID,
			PublishedAt: func() int64 {
				if p.PublishedAt.IsZero() {
					return 0
				}
				return p.PublishedAt.Unix()
			}(),
		},
		Content: LangContent(p.ContentJSON, locale),
	}, nil
}

// ListPostCategories 分类列表。
func (s *StoreContentService) ListPostCategories(ctx context.Context, _ *storefrontv1.ListPostCategoriesRequest) (*storefrontv1.ListPostCategoriesReply, error) {
	rows, err := s.repo.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	reply := &storefrontv1.ListPostCategoriesReply{}
	for _, c := range rows {
		reply.Categories = append(reply.Categories, &storefrontv1.StorePostCategory{
			Id: c.ID, Name: c.Name, Slug: c.Slug,
		})
	}
	return reply, nil
}
