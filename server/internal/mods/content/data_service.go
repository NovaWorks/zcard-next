package content

// 管理面 API（P2-04 T2）：banner/post/category CRUD + 发布状态机。

import (
	"context"
	"encoding/json"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"

	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminContentService 管理面内容服务。
type AdminContentService struct {
	adminv1.UnimplementedAdminContentServiceServer
	repo *ContentRepo
}

// NewAdminContentService 构造。
func NewAdminContentService(repo *ContentRepo) *AdminContentService {
	return &AdminContentService{repo: repo}
}

// ── Banner ────────────────────────────────────────────────

func (s *AdminContentService) CreateBanner(ctx context.Context, req *adminv1.CreateBannerRequest) (*adminv1.Banner, error) {
	b, err := s.repo.CreateBanner(ctx, bannerInputFromCreate(req))
	if err != nil {
		return nil, err
	}
	return toBannerPB(b), nil
}

func (s *AdminContentService) UpdateBanner(ctx context.Context, req *adminv1.UpdateBannerRequest) (*adminv1.Banner, error) {
	b, err := s.repo.UpdateBanner(ctx, req.GetId(), bannerInputFromUpdate(req))
	if err != nil {
		return nil, err
	}
	return toBannerPB(b), nil
}

func (s *AdminContentService) DeleteBanner(ctx context.Context, req *adminv1.DeleteBannerRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteBanner(ctx, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *AdminContentService) ListBanners(ctx context.Context, req *adminv1.ListBannersRequest) (*adminv1.ListBannersReply, error) {
	page, size := pageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListBanners(ctx, req.GetPosition(), page, size)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListBannersReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, b := range rows {
		reply.Banners = append(reply.Banners, toBannerPB(b))
	}
	return reply, nil
}

// ── Post ──────────────────────────────────────────────────

func (s *AdminContentService) CreatePost(ctx context.Context, req *adminv1.CreatePostRequest) (*adminv1.Post, error) {
	typ := orDefault(req.GetType(), "blog")
	p, err := s.repo.CreatePost(ctx, PostInput{
		Slug:        req.GetSlug(),
		Type:        typ,
		TitleJSON:   req.GetTitleJson(),
		SummaryJSON: req.GetSummaryJson(),
		ContentJSON: req.GetContentJson(),
		Thumbnail:   req.GetThumbnail(),
		CategoryID:  req.GetCategoryId(),
		IsPublished: req.GetIsPublished(),
	})
	if err != nil {
		return nil, err
	}
	return toPostPB(p), nil
}

func (s *AdminContentService) UpdatePost(ctx context.Context, req *adminv1.UpdatePostRequest) (*adminv1.Post, error) {
	p, err := s.repo.UpdatePost(ctx, req.GetId(), PostInput{
		TitleJSON:   req.GetTitleJson(),
		SummaryJSON: req.GetSummaryJson(),
		ContentJSON: req.GetContentJson(),
		Thumbnail:   req.GetThumbnail(),
		CategoryID:  req.GetCategoryId(),
	})
	if err != nil {
		return nil, err
	}
	return toPostPB(p), nil
}

func (s *AdminContentService) PublishPost(ctx context.Context, req *adminv1.PublishPostRequest) (*adminv1.Post, error) {
	p, err := s.repo.SetPublished(ctx, req.GetId(), req.GetPublish())
	if err != nil {
		return nil, err
	}
	return toPostPB(p), nil
}

func (s *AdminContentService) DeletePost(ctx context.Context, req *adminv1.DeletePostRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeletePost(ctx, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *AdminContentService) ListPosts(ctx context.Context, req *adminv1.ListPostsRequest) (*adminv1.ListPostsReply, error) {
	page, size := pageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListPosts(ctx, req.GetType(), page, size)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListPostsReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, p := range rows {
		reply.Posts = append(reply.Posts, toPostPB(p))
	}
	return reply, nil
}

// ── Category ──────────────────────────────────────────────

func (s *AdminContentService) CreateCategory(ctx context.Context, req *adminv1.CreatePostCategoryRequest) (*adminv1.PostCategory, error) {
	c, err := s.repo.CreateCategory(ctx, req.GetName(), req.GetSlug(), req.GetSort())
	if err != nil {
		return nil, err
	}
	return toCategoryPB(c), nil
}

func (s *AdminContentService) UpdateCategory(ctx context.Context, req *adminv1.UpdatePostCategoryRequest) (*adminv1.PostCategory, error) {
	c, err := s.repo.UpdateCategory(ctx, req.GetId(), req.GetName(), req.GetSort())
	if err != nil {
		return nil, err
	}
	return toCategoryPB(c), nil
}

func (s *AdminContentService) DeleteCategory(ctx context.Context, req *adminv1.DeletePostCategoryRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteCategory(ctx, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *AdminContentService) ListCategories(ctx context.Context, _ *adminv1.ListPostCategoriesAdminRequest) (*adminv1.ListPostCategoriesAdminReply, error) {
	rows, err := s.repo.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListPostCategoriesAdminReply{}
	for _, c := range rows {
		reply.Categories = append(reply.Categories, toCategoryPB(c))
	}
	return reply, nil
}

// ── 转换 ──────────────────────────────────────────────────

func bannerInputFromCreate(req *adminv1.CreateBannerRequest) BannerInput {
	return BannerInput{
		Name: req.GetName(), Position: orDefault(req.GetPosition(), "top"),
		TitleJSON: req.GetTitleJson(), Image: req.GetImage(), MobileImage: req.GetMobileImage(),
		LinkType: orDefault(req.GetLinkType(), "url"), LinkValue: req.GetLinkValue(),
		IsActive: req.GetIsActive(), Sort: req.GetSort(),
		StartAt: unixTime(req.GetStartAt()), EndAt: unixTime(req.GetEndAt()),
	}
}

func bannerInputFromUpdate(req *adminv1.UpdateBannerRequest) BannerInput {
	return BannerInput{
		Name: req.GetName(), Position: req.GetPosition(),
		TitleJSON: req.GetTitleJson(), Image: req.GetImage(), MobileImage: req.GetMobileImage(),
		LinkType: req.GetLinkType(), LinkValue: req.GetLinkValue(),
		IsActive: req.GetIsActive(), Sort: req.GetSort(),
		StartAt: unixTime(req.GetStartAt()), EndAt: unixTime(req.GetEndAt()),
	}
}

func toBannerPB(b *ent.Banner) *adminv1.Banner {
	p := &adminv1.Banner{
		Id: b.ID, Name: b.Name, Position: string(b.Position),
		Image: b.Image, MobileImage: b.MobileImage,
		LinkType: string(b.LinkType), LinkValue: b.LinkValue,
		IsActive: b.IsActive, Sort: b.Sort,
		CreatedAt: b.CreatedAt.Unix(), UpdatedAt: b.UpdatedAt.Unix(),
	}
	if b.TitleJSON != nil {
		if raw, err := json.Marshal(b.TitleJSON); err == nil {
			p.TitleJson = string(raw)
		}
	}
	if !b.StartAt.IsZero() {
		p.StartAt = b.StartAt.Unix()
	}
	if !b.EndAt.IsZero() {
		p.EndAt = b.EndAt.Unix()
	}
	return p
}

func toPostPB(p *ent.Post) *adminv1.Post {
	out := &adminv1.Post{
		Id: p.ID, Slug: p.Slug, Type: string(p.Type),
		ContentJson: p.ContentJSON, Thumbnail: p.Thumbnail,
		CategoryId: p.CategoryID, IsPublished: p.IsPublished,
		CreatedAt: p.CreatedAt.Unix(), UpdatedAt: p.UpdatedAt.Unix(),
	}
	if p.TitleJSON != nil {
		if raw, err := json.Marshal(p.TitleJSON); err == nil {
			out.TitleJson = string(raw)
		}
	}
	if p.SummaryJSON != nil {
		if raw, err := json.Marshal(p.SummaryJSON); err == nil {
			out.SummaryJson = string(raw)
		}
	}
	if !p.PublishedAt.IsZero() {
		out.PublishedAt = p.PublishedAt.Unix()
	}
	return out
}

func toCategoryPB(c *ent.PostCategory) *adminv1.PostCategory {
	return &adminv1.PostCategory{
		Id: c.ID, Name: c.Name, Slug: c.Slug, Sort: c.Sort, CreatedAt: c.CreatedAt.Unix(),
	}
}

func pageParams(page, pageSize int32) (int, int) {
	p := int(page)
	if p < 1 {
		p = 1
	}
	ps := int(pageSize)
	if ps < 1 {
		ps = 20
	}
	if ps > 100 {
		ps = 100
	}
	return p, ps
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func unixTime(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}
