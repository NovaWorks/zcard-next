package media

// 管理面 API（P3-06 T3）+ Uploader/Referencer port 实现。

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	mediaport "github.com/NovaWorks/zcard-next/server/internal/mods/media/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/httpx"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminMediaService 管理面素材服务。
type AdminMediaService struct {
	adminv1.UnimplementedAdminMediaServiceServer
	repo *MediaRepo
}

// NewAdminMediaService 构造。
func NewAdminMediaService(repo *MediaRepo) *AdminMediaService {
	return &AdminMediaService{repo: repo}
}

// Upload 实现 port.Uploader（业务模块与 admin API 共用同一安全路径）。
func (r *MediaRepo) Upload(ctx context.Context, in mediaport.UploadInput) (*mediaport.UploadResult, error) {
	clean, mime, w, h, err := ValidateAndReencode(in.Name, in.ContentType, in.Data)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(path.Ext(in.Name))
	rel, err := SaveLocal(clean, ext)
	if err != nil {
		return nil, err
	}
	m, err := r.CreateMedia(ctx, in, rel, mime, int64(len(clean)), int32(w), int32(h), HashFile(clean))
	if err != nil {
		_ = DeleteLocal(rel) // 回滚物理文件
		return nil, err
	}
	return &mediaport.UploadResult{ID: m.ID, Path: "/uploads/" + rel, Width: int32(w), Height: int32(h)}, nil
}

// Upload admin 上传。
func (s *AdminMediaService) Upload(ctx context.Context, req *adminv1.UploadMediaRequest) (*adminv1.MediaItem, error) {
	data, err := base64.StdEncoding.DecodeString(req.GetDataBase64())
	if err != nil {
		return nil, errors.BadRequest("media.BASE64_INVALID", "data_base64 解码失败")
	}
	res, err := s.repo.Upload(ctx, mediaport.UploadInput{
		Name: req.GetName(), ContentType: req.GetContentType(), Data: data,
		CategoryID: req.GetCategoryId(), UploaderID: adminUID(ctx),
	})
	if err != nil {
		return nil, mapMediaError(err)
	}
	m, _ := s.repo.GetMedia(ctx, res.ID)
	return toMediaPB(m), nil
}

// ImportFromURL 外链导入（httpx SSRF 防护 + 同三件套）。
func (s *AdminMediaService) ImportFromURL(ctx context.Context, req *adminv1.ImportMediaRequest) (*adminv1.MediaItem, error) {
	if err := httpx.ValidateURL(req.GetUrl()); err != nil {
		return nil, errors.BadRequest("media.URL_BLOCKED", "外链地址被 SSRF 防护拦截")
	}
	client := httpx.NewSafeClient(15 * time.Second) // 15s
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.GetUrl(), nil)
	if err != nil {
		return nil, errors.BadRequest("media.URL_INVALID", err.Error())
	}
	httpReq.Header.Set("User-Agent", httpx.UserAgent)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, errors.BadRequest("media.FETCH_FAILED", err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.BadRequest("media.FETCH_FAILED", fmt.Sprintf("外链返回 %d", resp.StatusCode))
	}
	// 限长读取（超 10MB 即拒，防拉爆内存）
	limited := io.LimitReader(resp.Body, MaxSizeBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, errors.BadRequest("media.FETCH_FAILED", err.Error())
	}
	name := path.Base(strings.Split(req.GetUrl(), "?")[0])
	if name == "" || name == "/" || !strings.Contains(name, ".") {
		name = "import.png" // 无扩展名兜底（魔数校验仍把关）
	}
	res, err := s.repo.Upload(ctx, mediaport.UploadInput{
		Name: name, ContentType: resp.Header.Get("Content-Type"), Data: data,
		CategoryID: req.GetCategoryId(), UploaderID: adminUID(ctx),
	})
	if err != nil {
		return nil, mapMediaError(err)
	}
	m, _ := s.repo.GetMedia(ctx, res.ID)
	return toMediaPB(m), nil
}

// ── 分类 ──────────────────────────────────────────────────

func (s *AdminMediaService) CreateCategory(ctx context.Context, req *adminv1.CreateMediaCategoryRequest) (*adminv1.MediaCategory, error) {
	c, err := s.repo.CreateCategory(ctx, req.GetName(), req.GetParentId(), req.GetSort())
	if err != nil {
		return nil, mapMediaError(err)
	}
	return toCategoryPB(c), nil
}

func (s *AdminMediaService) RenameCategory(ctx context.Context, req *adminv1.RenameMediaCategoryRequest) (*adminv1.MediaCategory, error) {
	c, err := s.repo.RenameCategory(ctx, req.GetId(), req.GetName())
	if err != nil {
		return nil, mapMediaError(err)
	}
	return toCategoryPB(c), nil
}

func (s *AdminMediaService) MoveCategory(ctx context.Context, req *adminv1.MoveMediaCategoryRequest) (*emptypb.Empty, error) {
	if err := s.repo.MoveCategory(ctx, req.GetId(), req.GetParentId()); err != nil {
		return nil, mapMediaError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *AdminMediaService) DeleteCategory(ctx context.Context, req *adminv1.DeleteMediaCategoryRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteCategory(ctx, req.GetId()); err != nil {
		return nil, mapMediaError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *AdminMediaService) ListCategories(ctx context.Context, _ *emptypb.Empty) (*adminv1.ListMediaCategoriesReply, error) {
	rows, err := s.repo.ListCategories(ctx)
	if err != nil {
		return nil, mapMediaError(err)
	}
	reply := &adminv1.ListMediaCategoriesReply{}
	for _, c := range rows {
		reply.Categories = append(reply.Categories, toCategoryPB(c))
	}
	return reply, nil
}

// ── 素材 ──────────────────────────────────────────────────

func (s *AdminMediaService) ListMedia(ctx context.Context, req *adminv1.ListMediaRequest) (*adminv1.ListMediaReply, error) {
	page, size := pageParams(req.GetPage(), req.GetPageSize())
	var (
		rows  []*ent.Media
		total int
		err   error
	)
	if req.GetUncategorized() {
		rows, total, err = s.repo.ListUncategorized(ctx, req.GetKeyword(), page, size)
	} else {
		rows, total, err = s.repo.ListMedia(ctx, req.GetCategoryId(), req.GetKeyword(), page, size)
	}
	if err != nil {
		return nil, mapMediaError(err)
	}
	reply := &adminv1.ListMediaReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, m := range rows {
		reply.Items = append(reply.Items, toMediaPB(m))
	}
	return reply, nil
}

func (s *AdminMediaService) RenameMedia(ctx context.Context, req *adminv1.RenameMediaRequest) (*adminv1.MediaItem, error) {
	m, err := s.repo.RenameMedia(ctx, req.GetId(), req.GetName())
	if err != nil {
		return nil, mapMediaError(err)
	}
	return toMediaPB(m), nil
}

func (s *AdminMediaService) MoveMedia(ctx context.Context, req *adminv1.MoveMediaRequest) (*emptypb.Empty, error) {
	if _, err := s.repo.MoveMedia(ctx, req.GetIds(), req.GetCategoryId()); err != nil {
		return nil, mapMediaError(err)
	}
	return &emptypb.Empty{}, nil
}

// DeleteMedia 批量删除（被引用未 confirm → 409 + 引用清单）。
func (s *AdminMediaService) DeleteMedia(ctx context.Context, req *adminv1.DeleteMediaRequest) (*adminv1.DeleteMediaReply, error) {
	deleted, refs, err := s.repo.DeleteMedia(ctx, req.GetIds(), req.GetConfirm())
	if err != nil {
		if err == ErrReferenced {
			reply := &adminv1.DeleteMediaReply{Deleted: 0, NeedConfirm: true}
			for _, m := range refs {
				reply.Referenced = append(reply.Referenced, toMediaPB(m))
			}
			return reply, nil // 200 + need_confirm（前端二次确认交互）
		}
		return nil, mapMediaError(err)
	}
	return &adminv1.DeleteMediaReply{Deleted: int32(deleted)}, nil
}

// ── 转换与工具 ────────────────────────────────────────────

func toMediaPB(m *ent.Media) *adminv1.MediaItem {
	if m == nil {
		return &adminv1.MediaItem{}
	}
	return &adminv1.MediaItem{
		Id: m.ID, CategoryId: m.CategoryID, Name: m.Name,
		Url: "/uploads/" + m.Path, Mime: m.Mime, Size: m.Size,
		Width: m.Width, Height: m.Height, RefCount: m.RefCount,
		CreatedAt: m.CreatedAt.Unix(),
	}
}

func toCategoryPB(c *ent.MediaCategory) *adminv1.MediaCategory {
	return &adminv1.MediaCategory{Id: c.ID, ParentId: c.ParentID, Name: c.Name, Sort: c.Sort}
}

func mapMediaError(err error) error {
	switch {
	case err == ErrCategoryName:
		return errors.BadRequest("media.CATEGORY_NAME_INVALID", "分类名 1-30 字符")
	case err == ErrHasChildren:
		return errors.BadRequest("media.CATEGORY_NOT_EMPTY", "分类存在子分类或素材")
	case err == ErrNotFound:
		return errors.NotFound("media.NOT_FOUND", "记录不存在")
	case err == ErrInvalidType:
		return errors.BadRequest("media.INVALID_TYPE", "仅支持 jpg/png/webp/gif 图片")
	case err == ErrTooLarge:
		return errors.BadRequest("media.TOO_LARGE", "超过 10MB 上限")
	case err == ErrNotImage:
		return errors.BadRequest("media.NOT_IMAGE", "文件内容不是合法图片")
	}
	return errors.InternalServer("media.ERROR", err.Error())
}

func adminUID(ctx context.Context) uint64 {
	if claims := identity.ClaimsFromContext(ctx); claims != nil {
		return claims.Subject
	}
	return 0
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
