package media

// 数据仓储（）：media/media_categories CRUD + 引用计数。

import (
	"context"
	"errors"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/media"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/mediacategory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/media/port"
)

// 哨兵错误。
var (
	ErrNotFound     = errors.New("media: 记录不存在")
	ErrCategoryName = errors.New("media.CATEGORY_NAME_INVALID: 名称 1-30 字符")
	ErrHasChildren  = errors.New("media: 分类存在子分类或素材，禁止删除")
	ErrReferenced   = errors.New("media.REFERENCED: 素材被引用，删除需 confirm")
)

// MediaRepo 仓储。
type MediaRepo struct {
	data *data.Data
}

// NewMediaRepo 构造。
func NewMediaRepo(d *data.Data) *MediaRepo { return &MediaRepo{data: d} }

// ── 分类 ──────────────────────────────────────────────────

// CreateCategory 建分类（名称 1-30 字符）。
func (r *MediaRepo) CreateCategory(ctx context.Context, name string, parentID uint64, sort int32) (*ent.MediaCategory, error) {
	if len([]rune(name)) < 1 || len([]rune(name)) > 30 {
		return nil, ErrCategoryName
	}
	create := data.Client(ctx, r.data).MediaCategory.Create().
		SetName(name).SetSort(sort)
	if parentID > 0 {
		create.SetParentID(parentID)
	}
	return create.Save(ctx)
}

// RenameCategory 改名。
func (r *MediaRepo) RenameCategory(ctx context.Context, id uint64, name string) (*ent.MediaCategory, error) {
	if len([]rune(name)) < 1 || len([]rune(name)) > 30 {
		return nil, ErrCategoryName
	}
	c, err := data.Client(ctx, r.data).MediaCategory.UpdateOneID(id).SetName(name).Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

// MoveCategory 移动（parentID=0 移到根；环状拒绝）。
func (r *MediaRepo) MoveCategory(ctx context.Context, id, parentID uint64) error {
	client := data.Client(ctx, r.data)
	if id == parentID {
		return fmt.Errorf("media.CATEGORY_CYCLE")
	}
	// 环检测：向上追溯 parent 链
	cur := parentID
	for cur != 0 {
		if cur == id {
			return fmt.Errorf("media.CATEGORY_CYCLE")
		}
		p, err := client.MediaCategory.Get(ctx, cur)
		if err != nil {
			break
		}
		cur = p.ParentID
	}
	upd := client.MediaCategory.UpdateOneID(id)
	if parentID > 0 {
		upd.SetParentID(parentID)
	} else {
		upd.ClearParentID()
	}
	_, err := upd.Save(ctx)
	return err
}

// DeleteCategory 删除（有子分类或有素材时拒绝）。
func (r *MediaRepo) DeleteCategory(ctx context.Context, id uint64) error {
	client := data.Client(ctx, r.data)
	children, err := client.MediaCategory.Query().Where(mediacategory.ParentID(id)).Count(ctx)
	if err != nil {
		return err
	}
	files, err := client.Media.Query().Where(media.CategoryID(id)).Count(ctx)
	if err != nil {
		return err
	}
	if children > 0 || files > 0 {
		return ErrHasChildren
	}
	err = client.MediaCategory.DeleteOneID(id).Exec(ctx)
	if err != nil && ent.IsNotFound(err) {
		return ErrNotFound
	}
	return err
}

// ListCategories 分类树（扁平全量，前端组树）。
func (r *MediaRepo) ListCategories(ctx context.Context) ([]*ent.MediaCategory, error) {
	return data.Client(ctx, r.data).MediaCategory.Query().
		Order(ent.Asc(mediacategory.FieldSort)).
		All(ctx)
}

// ── 素材 ──────────────────────────────────────────────────

// CreateMedia 入库（净化后）。
func (r *MediaRepo) CreateMedia(ctx context.Context, in port.UploadInput, relPath, mime string, size int64, width, height int32, sha string) (*ent.Media, error) {
	create := data.Client(ctx, r.data).Media.Create().
		SetPath(relPath).
		SetName(in.Name).
		SetMime(mime).
		SetSize(size).
		SetSha256(sha).
		SetStorage(media.StorageLocal)
	if in.CategoryID > 0 {
		create.SetCategoryID(in.CategoryID)
	}
	if in.UploaderID > 0 {
		create.SetUploaderID(in.UploaderID)
	}
	if width > 0 {
		create.SetWidth(width)
	}
	if height > 0 {
		create.SetHeight(height)
	}
	return create.Save(ctx)
}

// ListMedia 列表（分类过滤/关键词/分页）。
func (r *MediaRepo) ListMedia(ctx context.Context, categoryID uint64, keyword string, page, size int) ([]*ent.Media, int, error) {
	q := data.Client(ctx, r.data).Media.Query().Order(ent.Desc(media.FieldID))
	if categoryID > 0 {
		q = q.Where(media.CategoryID(categoryID))
	}
	if keyword != "" {
		q = q.Where(media.NameContains(keyword))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// ListUncategorized 仅未分类素材（category_id 为空；素材选择器「未分类」视图）。
func (r *MediaRepo) ListUncategorized(ctx context.Context, keyword string, page, size int) ([]*ent.Media, int, error) {
	q := data.Client(ctx, r.data).Media.Query().
		Where(media.CategoryIDIsNil()).
		Order(ent.Desc(media.FieldID))
	if keyword != "" {
		q = q.Where(media.NameContains(keyword))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// GetMedia 单个。
func (r *MediaRepo) GetMedia(ctx context.Context, id uint64) (*ent.Media, error) {
	m, err := data.Client(ctx, r.data).Media.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

// RenameMedia 改名（显示名）。
func (r *MediaRepo) RenameMedia(ctx context.Context, id uint64, name string) (*ent.Media, error) {
	if name == "" || len([]rune(name)) > 255 {
		return nil, fmt.Errorf("media.NAME_INVALID")
	}
	m, err := data.Client(ctx, r.data).Media.UpdateOneID(id).SetName(name).Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

// MoveMedia 批量移动分类。
func (r *MediaRepo) MoveMedia(ctx context.Context, ids []uint64, categoryID uint64) (int, error) {
	client := data.Client(ctx, r.data)
	q := client.Media.Update().Where(media.IDIn(ids...))
	if categoryID > 0 {
		q = q.SetCategoryID(categoryID)
	} else {
		q = q.ClearCategoryID()
	}
	n, err := q.Save(ctx)
	return n, err
}

// ListReferenced 被引用素材清单（删除前展示）。
func (r *MediaRepo) ListReferenced(ctx context.Context, ids []uint64) ([]*ent.Media, error) {
	return data.Client(ctx, r.data).Media.Query().
		Where(media.IDIn(ids...), media.RefCountGT(0)).
		All(ctx)
}

// DeleteMedia 删除（ref_count>0 且未 confirm → 拒绝并返回清单语义由 service 层组装）。
func (r *MediaRepo) DeleteMedia(ctx context.Context, ids []uint64, force bool) (deleted int, refs []*ent.Media, err error) {
	client := data.Client(ctx, r.data)
	// 引用检查
	referenced, err := client.Media.Query().
		Where(media.IDIn(ids...), media.RefCountGT(0)).All(ctx)
	if err != nil {
		return 0, nil, err
	}
	if len(referenced) > 0 && !force {
		return 0, referenced, ErrReferenced
	}
	// 物理路径先收集（删行后不可回查）
	rows, err := client.Media.Query().Where(media.IDIn(ids...)).All(ctx)
	if err != nil {
		return 0, nil, err
	}
	n, err := client.Media.Delete().Where(media.IDIn(ids...)).Exec(ctx)
	if err != nil {
		return 0, nil, err
	}
	for _, m := range rows {
		_ = DeleteLocal(m.Path)
	}
	// 物理文件清理（先取 path 再删行——顺序修正；失败不阻断，垃圾文件 cron 兜底）
	return n, nil, nil
}

// ── 引用计数（port.Referencer 实现）──────────────────────

// AddRefs 引用 +1。
func (r *MediaRepo) AddRefs(ctx context.Context, ids []uint64) error {
	return r.adjustRefs(ctx, ids, +1)
}

// ReleaseRefs 引用 -1（下限 0）。
func (r *MediaRepo) ReleaseRefs(ctx context.Context, ids []uint64) error {
	return r.adjustRefs(ctx, ids, -1)
}

func (r *MediaRepo) adjustRefs(ctx context.Context, ids []uint64, delta int32) error {
	if len(ids) == 0 {
		return nil
	}
	client := data.Client(ctx, r.data)
	rows, err := client.Media.Query().Where(media.IDIn(ids...)).All(ctx)
	if err != nil {
		return err
	}
	for _, m := range rows {
		next := m.RefCount + delta
		if next < 0 {
			next = 0 // 下限 0（释放多于持有属调用方 bug，钳制防负）
		}
		if _, err := client.Media.UpdateOneID(m.ID).SetRefCount(next).Save(ctx); err != nil {
			return err
		}
	}
	return nil
}
