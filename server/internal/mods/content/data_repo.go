package content

// 数据仓储（P2-04）：banner/post/category CRUD + 时间窗查询 + 多语言回落。
// sanitize 纪律：content_json（HTML）入库前经 platform/sanitize.HTML。

import (
	"strings"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/banner"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/post"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/postcategory"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/media"
	mediaport "github.com/NovaWorks/zcard-next/server/internal/mods/media/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/sanitize"
)

// 哨兵错误。
var (
	ErrNotFound       = errors.New("content: 记录不存在")
	ErrSlugDuplicated = errors.New("content: slug 已存在")
	ErrCategoryInUse  = errors.New("content: 分类下存在文章，禁止删除")
)

// ContentRepo 内容仓储。
type ContentRepo struct {
	data     *data.Data
	mediaRef mediaport.Referencer // 横幅/文章配图引用（nil 跳过）
}

// NewContentRepo 构造（mediaRef 素材引用计数，P3-06）。
func NewContentRepo(d *data.Data, mediaRef mediaport.Referencer) *ContentRepo {
	return &ContentRepo{data: d, mediaRef: mediaRef}
}

// adjustBannerRefs 横幅配图引用（旧释放新引用：image + mobile_image）。
func (r *ContentRepo) adjustBannerRefs(ctx context.Context, oldImage, oldMobile, newImage, newMobile string) {
	if r.mediaRef == nil {
		return
	}
	_ = r.mediaRef.ReleaseRefs(ctx, mediaIDsOf(ctx, r.data, oldImage, oldMobile))
	_ = r.mediaRef.AddRefs(ctx, mediaIDsOf(ctx, r.data, newImage, newMobile))
}

// ── Banner ────────────────────────────────────────────────

// BannerInput banner 写入参数。
type BannerInput struct {
	Name        string
	Position    string
	TitleJSON   string
	Image       string
	MobileImage string
	LinkType    string
	LinkValue   string
	IsActive    bool
	StartAt     time.Time
	EndAt       time.Time
	Sort        int32
}

// CreateBanner 创建。
func (r *ContentRepo) CreateBanner(ctx context.Context, in BannerInput) (*ent.Banner, error) {
	title, err := mustLangJSON(in.TitleJSON)
	if err != nil {
		return nil, err
	}
	create := data.Client(ctx, r.data).Banner.Create().
		SetName(in.Name).
		SetPosition(banner.Position(in.Position)).
		SetTitleJSON(title).
		SetImage(in.Image).
		SetLinkType(banner.LinkType(in.LinkType)).
		SetIsActive(in.IsActive).
		SetSort(in.Sort)
	if in.MobileImage != "" {
		create.SetMobileImage(in.MobileImage)
	}
	if in.LinkValue != "" {
		create.SetLinkValue(in.LinkValue)
	}
	if !in.StartAt.IsZero() {
		create.SetStartAt(in.StartAt)
	}
	if !in.EndAt.IsZero() {
		create.SetEndAt(in.EndAt)
	}
	b, err := create.Save(ctx)
	if err == nil {
		r.adjustBannerRefs(ctx, "", "", in.Image, in.MobileImage)
	}
	return b, err
}

// UpdateBanner 更新（零值字段不动）。
func (r *ContentRepo) UpdateBanner(ctx context.Context, id uint64, in BannerInput) (*ent.Banner, error) {
	existing, err := data.Client(ctx, r.data).Banner.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	upd := data.Client(ctx, r.data).Banner.UpdateOneID(id).
		SetIsActive(in.IsActive).SetSort(in.Sort)
	if in.Name != "" {
		upd.SetName(in.Name)
	}
	if in.Position != "" {
		upd.SetPosition(banner.Position(in.Position))
	}
	if in.TitleJSON != "" {
		title, err := mustLangJSON(in.TitleJSON)
		if err != nil {
			return nil, err
		}
		upd.SetTitleJSON(title)
	}
	if in.Image != "" {
		upd.SetImage(in.Image)
	}
	if in.MobileImage != "" {
		upd.SetMobileImage(in.MobileImage)
	}
	if in.LinkType != "" {
		upd.SetLinkType(banner.LinkType(in.LinkType))
	}
	if in.LinkValue != "" {
		upd.SetLinkValue(in.LinkValue)
	}
	if !in.StartAt.IsZero() {
		upd.SetStartAt(in.StartAt)
	}
	if !in.EndAt.IsZero() {
		upd.SetEndAt(in.EndAt)
	}
	b, err := upd.Save(ctx)
	if err == nil && existing != nil {
		r.adjustBannerRefs(ctx, existing.Image, existing.MobileImage, in.Image, in.MobileImage)
	}
	return b, err
}

// DeleteBanner 删除。
func (r *ContentRepo) DeleteBanner(ctx context.Context, id uint64) error {
	err := data.Client(ctx, r.data).Banner.DeleteOneID(id).Exec(ctx)
	if err != nil && ent.IsNotFound(err) {
		return ErrNotFound
	}
	return err
}

// ListBanners 管理面列表（全量含停用；position 过滤）。
func (r *ContentRepo) ListBanners(ctx context.Context, position string, page, pageSize int) ([]*ent.Banner, int, error) {
	q := data.Client(ctx, r.data).Banner.Query().
		Order(ent.Asc(banner.FieldSort), ent.Desc(banner.FieldID))
	if position != "" {
		q = q.Where(banner.PositionEQ(banner.Position(position)))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * pageSize).Limit(pageSize).All(ctx)
	return rows, total, err
}

// ListActiveBanners 生效中横幅（is_active + 时间窗三态过滤 + sort）。
func (r *ContentRepo) ListActiveBanners(ctx context.Context, position string) ([]*ent.Banner, error) {
	now := time.Now().UTC()
	q := data.Client(ctx, r.data).Banner.Query().
		Where(banner.IsActive(true)).
		Order(ent.Asc(banner.FieldSort), ent.Desc(banner.FieldID))
	if position != "" {
		q = q.Where(banner.PositionEQ(banner.Position(position)))
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	// 时间窗三态过滤（start/end 均可选）
	out := make([]*ent.Banner, 0, len(rows))
	for _, b := range rows {
		if !b.StartAt.IsZero() && now.Before(b.StartAt) {
			continue // 未开始
		}
		if !b.EndAt.IsZero() && now.After(b.EndAt) {
			continue // 已结束
		}
		out = append(out, b)
	}
	return out, nil
}

// ── Post ──────────────────────────────────────────────────

// PostInput post 写入参数。
type PostInput struct {
	Slug        string
	Type        string
	TitleJSON   string
	SummaryJSON string
	ContentJSON string // 多语言内容（HTML，逐语言 sanitize）
	Thumbnail   string
	CategoryID  uint64
	IsPublished bool
}

// CreatePost 创建（slug 唯一；content 逐语言 sanitize）。
func (r *ContentRepo) CreatePost(ctx context.Context, in PostInput) (*ent.Post, error) {
	title, err := mustLangJSON(in.TitleJSON)
	if err != nil {
		return nil, err
	}
	content, err := sanitizeLangContent(in.ContentJSON)
	if err != nil {
		return nil, err
	}
	if content == "" {
		return nil, fmt.Errorf("content.CONTENT_REQUIRED")
	}
	create := data.Client(ctx, r.data).Post.Create().
		SetSlug(in.Slug).
		SetType(post.Type(in.Type)).
		SetTitleJSON(title).
		SetContentJSON(content).
		SetIsPublished(in.IsPublished)
	if in.SummaryJSON != "" {
		summary, err := mustLangJSON(in.SummaryJSON)
		if err != nil {
			return nil, err
		}
		create.SetSummaryJSON(summary)
	}
	if in.Thumbnail != "" {
		create.SetThumbnail(in.Thumbnail)
	}
	if in.CategoryID > 0 {
		create.SetCategoryID(in.CategoryID)
	}
	if in.IsPublished {
		create.SetPublishedAt(time.Now().UTC())
	}
	p, err := create.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, ErrSlugDuplicated
		}
		return nil, err
	}
	return p, nil
}

// UpdatePost 更新（slug/type 不改；内容逐语言 sanitize）。
func (r *ContentRepo) UpdatePost(ctx context.Context, id uint64, in PostInput) (*ent.Post, error) {
	if _, err := data.Client(ctx, r.data).Post.Get(ctx, id); err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	upd := data.Client(ctx, r.data).Post.UpdateOneID(id)
	if in.TitleJSON != "" {
		title, err := mustLangJSON(in.TitleJSON)
		if err != nil {
			return nil, err
		}
		upd.SetTitleJSON(title)
	}
	if in.SummaryJSON != "" {
		summary, err := mustLangJSON(in.SummaryJSON)
		if err != nil {
			return nil, err
		}
		upd.SetSummaryJSON(summary)
	}
	if in.ContentJSON != "" {
		content, err := sanitizeLangContent(in.ContentJSON)
		if err != nil {
			return nil, err
		}
		if content != "" {
			upd.SetContentJSON(content)
		}
	}
	if in.Thumbnail != "" {
		upd.SetThumbnail(in.Thumbnail)
	}
	if in.CategoryID > 0 {
		upd.SetCategoryID(in.CategoryID)
	}
	return upd.Save(ctx)
}

// SetPublished 发布/取消（首发回填 published_at）。
func (r *ContentRepo) SetPublished(ctx context.Context, id uint64, publish bool) (*ent.Post, error) {
	existing, err := data.Client(ctx, r.data).Post.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	upd := data.Client(ctx, r.data).Post.UpdateOneID(id).SetIsPublished(publish)
	if publish && existing.PublishedAt.IsZero() {
		upd.SetPublishedAt(time.Now().UTC()) // 首发回填（取消再发布不覆盖）
	}
	return upd.Save(ctx)
}

// DeletePost 删除。
func (r *ContentRepo) DeletePost(ctx context.Context, id uint64) error {
	err := data.Client(ctx, r.data).Post.DeleteOneID(id).Exec(ctx)
	if err != nil && ent.IsNotFound(err) {
		return ErrNotFound
	}
	return err
}

// ListPosts 管理面列表（全量含草稿）。
func (r *ContentRepo) ListPosts(ctx context.Context, typ string, page, pageSize int) ([]*ent.Post, int, error) {
	q := data.Client(ctx, r.data).Post.Query().Order(ent.Desc(post.FieldID))
	if typ != "" {
		q = q.Where(post.TypeEQ(post.Type(typ)))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * pageSize).Limit(pageSize).All(ctx)
	return rows, total, err
}

// ListPublishedPosts 已发布文章分页。
func (r *ContentRepo) ListPublishedPosts(ctx context.Context, typ string, page, pageSize int) ([]*ent.Post, int, error) {
	q := data.Client(ctx, r.data).Post.Query().
		Where(post.IsPublished(true)).
		Order(ent.Desc(post.FieldPublishedAt))
	if typ != "" {
		q = q.Where(post.TypeEQ(post.Type(typ)))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * pageSize).Limit(pageSize).All(ctx)
	return rows, total, err
}

// GetPublishedBySlug 按 slug 取已发布文章。
func (r *ContentRepo) GetPublishedBySlug(ctx context.Context, slug string) (*ent.Post, error) {
	p, err := data.Client(ctx, r.data).Post.Query().
		Where(post.Slug(slug), post.IsPublished(true)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// ── PostCategory ──────────────────────────────────────────

// CreateCategory 创建分类（slug 唯一）。
func (r *ContentRepo) CreateCategory(ctx context.Context, name, slug string, sort int32) (*ent.PostCategory, error) {
	c, err := data.Client(ctx, r.data).PostCategory.Create().
		SetName(name).
		SetSlug(slug).
		SetSort(sort).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, ErrSlugDuplicated
		}
		return nil, err
	}
	return c, nil
}

// UpdateCategory 更新分类。
func (r *ContentRepo) UpdateCategory(ctx context.Context, id uint64, name string, sort int32) (*ent.PostCategory, error) {
	if _, err := data.Client(ctx, r.data).PostCategory.Get(ctx, id); err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	upd := data.Client(ctx, r.data).PostCategory.UpdateOneID(id).SetSort(sort)
	if name != "" {
		upd.SetName(name)
	}
	return upd.Save(ctx)
}

// DeleteCategory 删除分类（存在文章时拒绝）。
func (r *ContentRepo) DeleteCategory(ctx context.Context, id uint64) error {
	n, err := data.Client(ctx, r.data).Post.Query().
		Where(post.CategoryID(id)).
		Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrCategoryInUse
	}
	err = data.Client(ctx, r.data).PostCategory.DeleteOneID(id).Exec(ctx)
	if err != nil && ent.IsNotFound(err) {
		return ErrNotFound
	}
	return err
}

// ListCategories 分类列表（sort 升序）。
func (r *ContentRepo) ListCategories(ctx context.Context) ([]*ent.PostCategory, error) {
	return data.Client(ctx, r.data).PostCategory.Query().
		Order(ent.Asc(postcategory.FieldSort)).
		All(ctx)
}

// ── 多语言工具 ────────────────────────────────────────────

// mustLangJSON 校验并解析多语言 JSON（{"zh_CN": "...", "en": "..."}）。
func mustLangJSON(s string) (map[string]string, error) {
	if s == "" {
		return map[string]string{}, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("content.LOCALE_JSON_INVALID: %w", err)
	}
	return m, nil
}

// sanitizeLangContent 多语言内容逐语言 sanitize（HTML 白名单）。
// 输入 {"zh_CN": "<p>ok</p><script>x</script>", ...} → 输出同结构已剥离 XSS。
func sanitizeLangContent(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return "", fmt.Errorf("content.CONTENT_JSON_INVALID: %w", err)
	}
	out := make(map[string]string, len(m))
	for locale, html := range m {
		out[locale] = sanitize.HTML(html)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// LangValue 多语言回落取值（locale → zh_CN → 首个非空）。
func LangValue(m map[string]string, locale string) string {
	if v, ok := m[locale]; ok && v != "" {
		return v
	}
	if v, ok := m["zh_CN"]; ok && v != "" {
		return v
	}
	for _, v := range m {
		if v != "" {
			return v
		}
	}
	return ""
}

// LangContent 多语言内容回落（content_json 是 Text 存 JSON 字符串）。
func LangContent(contentJSON, locale string) string {
	var m map[string]string
	if err := json.Unmarshal([]byte(contentJSON), &m); err != nil {
		return contentJSON // 非结构化（防御）：原样返回
	}
	return LangValue(m, locale)
}

// mediaIDsOf URL 列表 → 素材 id（/uploads/ 前缀才计）。
func mediaIDsOf(ctx context.Context, d *data.Data, urls ...string) []uint64 {
	var ids []uint64
	for _, u := range urls {
		p := strings.TrimPrefix(u, "/uploads/")
		if p == u || p == "" {
			continue
		}
		if m, err := data.Client(ctx, d).Media.Query().Where(media.PathEQ(p)).Only(ctx); err == nil {
			ids = append(ids, m.ID)
		}
	}
	return ids
}
