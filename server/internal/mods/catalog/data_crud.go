package catalog

// 商品/分类/标签管理 CRUD 数据层（P1-01；ent import 收口：data 前缀文件）。
// sanitize 在 service 层调用后传入；slug 唯一校验在 biz。

import (
	"context"
	"fmt"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/tag"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// ── 商品 ─────────────────────────────────────────────────────

// ListAdmin 管理面商品列表（含下架/隐藏；成本价下发）。
func (r *ProductRepoImpl) ListAdmin(ctx context.Context, f port.AdminFilter) ([]*ent.Product, int64, error) {
	tc := tenancy.FromContext(ctx)
	q := data.Client(ctx, r.data).Product.Query().
		Where(product.SubsiteID(tc.SubsiteID)).
		Order(ent.Asc(product.FieldSort), ent.Desc(product.FieldID))
	if f.CategoryID > 0 {
		q = q.Where(product.CategoryID(f.CategoryID))
	}
	if f.Keyword != "" {
		q = q.Where(product.NameHasPrefix(f.Keyword))
	}
	if f.Status > 0 { // 0=全部（proto3 默认值）；>0 才过滤
		q = q.Where(product.Status(int8(f.Status)))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	if f.Page > 0 && f.PageSize > 0 {
		q = q.Offset((int(f.Page) - 1) * int(f.PageSize)).Limit(int(f.PageSize))
	}
	rows, err := q.All(ctx)
	return rows, int64(total), err
}

// GetAdmin 管理面商品详情。
func (r *ProductRepoImpl) GetAdmin(ctx context.Context, subsiteID, id uint64) (*ent.Product, error) {
	return data.Client(ctx, r.data).Product.Query().
		Where(product.ID(id), product.SubsiteID(subsiteID)).
		Only(ctx)
}

// CreateProduct 创建商品（description 已 sanitize）。
func (r *ProductRepoImpl) CreateProduct(ctx context.Context, in port.ProductInput) (*ent.Product, error) {
	tc := tenancy.FromContext(ctx)
	slug, err := r.genUniqueSlug(ctx, tc.SubsiteID, in.Name)
	if err != nil {
		return nil, err
	}
	create := data.Client(ctx, r.data).Product.Create().
		SetSubsiteID(tc.SubsiteID).
		SetName(in.Name).
		SetSlug(slug).
		SetPrice(in.Price).
		SetFactoryPrice(in.FactoryPrice).
		SetStockType(product.StockType(in.StockType))
	if in.DeliveryMode != "" {
		create = create.SetDeliveryMode(product.DeliveryMode(in.DeliveryMode))
	}
	create = create.SetStockVisible(in.StockVisible).SetDedup(in.Dedup).SetSort(in.Sort).SetStatus(in.Status)
	if in.CategoryID > 0 {
		create.SetCategoryID(in.CategoryID)
	}
	if in.Description != "" {
		create.SetDescription(in.Description)
	}
	if in.Cover != "" {
		create.SetCover(in.Cover)
	}
	if len(in.Images) > 0 {
		create.SetImages(in.Images)
	}
	return create.Save(ctx)
}

// UpdateProduct 更新（nil/零值字段不动）。
func (r *ProductRepoImpl) UpdateProduct(ctx context.Context, id uint64, in port.ProductInput) (*ent.Product, error) {
	q := data.Client(ctx, r.data).Product.UpdateOneID(id)
	if in.Name != "" {
		q.SetName(in.Name)
	}
	if in.CategoryID > 0 {
		q.SetCategoryID(in.CategoryID)
	}
	if in.Description != "" {
		q.SetDescription(in.Description)
	}
	if in.Cover != "" {
		q.SetCover(in.Cover)
	}
	if len(in.Images) > 0 {
		q.SetImages(in.Images)
	}
	if in.Price > 0 {
		q.SetPrice(in.Price)
	}
	if in.FactoryPrice >= 0 {
		q.SetFactoryPrice(in.FactoryPrice)
	}
	if in.DeliveryMode != "" {
		q.SetDeliveryMode(product.DeliveryMode(in.DeliveryMode))
	}
	q.SetStockVisible(in.StockVisible)
	if in.Sort >= 0 {
		q.SetSort(in.Sort)
	}
	if in.Status >= 0 {
		q.SetStatus(in.Status)
	}
	if err := q.Exec(ctx); err != nil {
		return nil, err
	}
	return r.GetAdmin(ctx, tenancy.FromContext(ctx).SubsiteID, id)
}

// DeleteProduct 删除（软外键约束：有卡密时拒删）。
func (r *ProductRepoImpl) DeleteProduct(ctx context.Context, id uint64) error {
	n, err := data.Client(ctx, r.data).Product.Delete().Where(product.ID(id)).Exec(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("catalog.PRODUCT_NOT_FOUND")
	}
	return nil
}

func (r *ProductRepoImpl) genUniqueSlug(ctx context.Context, subsiteID uint64, name string) (string, error) {
	base := slugify(name)
	if base == "" {
		base = fmt.Sprintf("p-%d", entutilID())
	}
	slug := base
	for i := 0; i < 100; i++ {
		exists, err := data.Client(ctx, r.data).Product.Query().
			Where(product.SubsiteID(subsiteID), product.Slug(slug)).Exist(ctx)
		if err != nil {
			return "", err
		}
		if !exists {
			return slug, nil
		}
		slug = fmt.Sprintf("%s-%d", base, i+2)
	}
	return "", fmt.Errorf("catalog.SLUG_EXHAUSTED")
}

func slugify(s string) string {
	var out []rune
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			out = append(out, r)
		} else if r == ' ' || r == '_' {
			out = append(out, '-')
		}
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return string(out)
}

var idCounter uint64

func entutilID() uint64 {
	idCounter++
	return idCounter
}

// ── 分类 ─────────────────────────────────────────────────────

// ListCategories 分类列表（含商品计数）。
func (r *ProductRepoImpl) ListCategories(ctx context.Context) ([]*ent.Category, error) {
	tc := tenancy.FromContext(ctx)
	return data.Client(ctx, r.data).Category.Query().
		Where(category.SubsiteID(tc.SubsiteID)).
		Order(ent.Asc(category.FieldSort), ent.Asc(category.FieldID)).
		All(ctx)
}

// CreateCategory 创建分类。
func (r *ProductRepoImpl) CreateCategory(ctx context.Context, name string, parentID uint64, icon string, sort int32) (*ent.Category, error) {
	tc := tenancy.FromContext(ctx)
	// 环状校验
	if parentID > 0 {
		if err := r.checkCategoryTree(ctx, parentID, 0); err != nil {
			return nil, err
		}
	}
	return data.Client(ctx, r.data).Category.Create().
		SetSubsiteID(tc.SubsiteID).
		SetName(name).
		SetNillableParentID(nilOrZero(parentID)).
		SetIcon(icon).
		SetSort(sort).
		Save(ctx)
}

// UpdateCategory 更新分类。
func (r *ProductRepoImpl) UpdateCategory(ctx context.Context, id uint64, name, icon string, hide bool, sort int32) (*ent.Category, error) {
	q := data.Client(ctx, r.data).Category.UpdateOneID(id)
	if name != "" {
		q.SetName(name)
	}
	if icon != "" {
		q.SetIcon(icon)
	}
	q.SetHide(hide)
	if sort >= 0 {
		q.SetSort(sort)
	}
	return q.Save(ctx)
}

// DeleteCategory 删除（有子分类或有商品时拒删）。
func (r *ProductRepoImpl) DeleteCategory(ctx context.Context, id uint64) error {
	client := data.Client(ctx, r.data)
	hasChildren, err := client.Category.Query().Where(category.ParentID(id)).Exist(ctx)
	if err != nil {
		return err
	}
	if hasChildren {
		return fmt.Errorf("catalog.CATEGORY_HAS_CHILDREN")
	}
	hasProducts, err := client.Product.Query().Where(product.CategoryID(id)).Exist(ctx)
	if err != nil {
		return err
	}
	if hasProducts {
		return fmt.Errorf("catalog.CATEGORY_HAS_PRODUCTS")
	}
	n, err := client.Category.Delete().Where(category.ID(id)).Exec(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("catalog.CATEGORY_NOT_FOUND")
	}
	return nil
}

func (r *ProductRepoImpl) checkCategoryTree(ctx context.Context, id, parentID uint64) error {
	if id == parentID {
		return fmt.Errorf("catalog.CATEGORY_CYCLE")
	}
	if parentID == 0 {
		return nil
	}
	row, err := data.Client(ctx, r.data).Category.Get(ctx, parentID)
	if ent.IsNotFound(err) {
		return fmt.Errorf("catalog.PARENT_NOT_FOUND")
	}
	if err != nil {
		return err
	}
	return r.checkCategoryTree(ctx, id, row.ParentID)
}

// ── 标签 ─────────────────────────────────────────────────────

// ListTags 标签列表。
func (r *ProductRepoImpl) ListTags(ctx context.Context) ([]*ent.Tag, error) {
	return data.Client(ctx, r.data).Tag.Query().Order(ent.Asc(tag.FieldID)).All(ctx)
}

// CreateTag 创建标签。
func (r *ProductRepoImpl) CreateTag(ctx context.Context, name, slug, icon, color, position string) (*ent.Tag, error) {
	return data.Client(ctx, r.data).Tag.Create().
		SetName(name).
		SetSlug(slug).
		SetIcon(icon).
		SetColor(color).
		SetPosition(tag.Position(position)).
		Save(ctx)
}

// DeleteTag 删除标签。
func (r *ProductRepoImpl) DeleteTag(ctx context.Context, id uint64) error {
	n, err := data.Client(ctx, r.data).Tag.Delete().Where(tag.ID(id)).Exec(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("catalog.TAG_NOT_FOUND")
	}
	return nil
}

// ── 管理面 DTO 转换 ──────────────────────────────────────────

// ToAdminPB 转 admin 协议对象。
func ToAdminPB(p *ent.Product) *adminv1.AdminProduct {
	out := &adminv1.AdminProduct{
		Id: p.ID, CategoryId: p.CategoryID, Name: p.Name, Slug: p.Slug,
		Description: p.Description, Cover: p.Cover, Images: p.Images,
		PriceCents: p.Price, FactoryPriceCents: p.FactoryPrice,
		StockType: string(p.StockType), StockVisible: p.StockVisible,
		DeliveryMode: string(p.DeliveryMode), Dedup: p.Dedup,
		Sort: p.Sort, Status: int32(p.Status),
		UpstreamSourceId: p.UpstreamSourceID, UpstreamProductCode: p.UpstreamProductCode,
	}
	if !p.CreatedAt.IsZero() {
		out.CreatedAt = p.CreatedAt.Unix()
	}
	if !p.UpdatedAt.IsZero() {
		out.UpdatedAt = p.UpdatedAt.Unix()
	}
	return out
}

func nilOrZero(v uint64) *uint64 {
	if v == 0 {
		return nil
	}
	return &v
}

// UpsertUpstreamProduct 货源同步商品 upsert（P2-01 T3，supply 模块经 port 消费）。
// 判据：subsite_id + upstream_source_id + upstream_product_code 幂等。
// Price=-1 保持现有价（价格保护由 supply 侧决策后传入）。
func (r *ProductRepoImpl) UpsertUpstreamProduct(ctx context.Context, in port.UpstreamProductInput) (uint64, bool, error) {
	tc := tenancy.FromContext(ctx)
	existing, err := data.Client(ctx, r.data).Product.Query().
		Where(
			product.SubsiteID(tc.SubsiteID),
			product.UpstreamSourceID(in.ConnectionID),
			product.UpstreamProductCode(in.UpstreamProductCode),
		).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return 0, false, err
	}

	if ent.IsNotFound(err) {
		// 新建：slug 用上游标识（稳定、幂等）；状态按 auto_onshelf 开关
		slug := slugify(in.UpstreamProductCode)
		if slug == "" {
			slug = fmt.Sprintf("up-%d-%s", in.ConnectionID, in.UpstreamProductCode)
		}
		baseSlug := slug
		for i := 0; i < 100; i++ {
			exists, err2 := data.Client(ctx, r.data).Product.Query().
				Where(product.SubsiteID(tc.SubsiteID), product.Slug(slug)).Exist(ctx)
			if err2 != nil {
				return 0, false, err2
			}
			if !exists {
				break
			}
			slug = fmt.Sprintf("%s-%d", baseSlug, i+2)
		}
		status := int8(0)
		if in.AutoOnshelf {
			status = 1
		}
		create := data.Client(ctx, r.data).Product.Create().
			SetSubsiteID(tc.SubsiteID).
			SetName(in.Name).
			SetSlug(slug).
			SetPrice(in.Price).
			SetFactoryPrice(in.FactoryPrice).
			SetStockType(product.StockTypeCard).
			SetStatus(status).
			SetUpstreamSourceID(in.ConnectionID).
			SetUpstreamProductCode(in.UpstreamProductCode).
			SetUpstreamSyncedAt(in.UpstreamSyncedAt)
		if in.CategoryID > 0 {
			create.SetCategoryID(in.CategoryID)
		}
		if in.Description != "" {
			create.SetDescription(in.Description)
		}
		if in.Cover != "" {
			create.SetCover(in.Cover)
		}
		created, err := create.Save(ctx)
		if err != nil {
			return 0, false, err
		}
		return created.ID, true, nil
	}

	// 更新：名称/描述/封面/分类/状态/成本价；价格按保护语义（-1 不动）
	upd := data.Client(ctx, r.data).Product.UpdateOneID(existing.ID).
		SetName(in.Name).
		SetFactoryPrice(in.FactoryPrice).
		SetUpstreamSyncedAt(in.UpstreamSyncedAt)
	if in.Price >= 0 {
		upd.SetPrice(in.Price)
	}
	if in.Status != 0 {
		upd.SetStatus(in.Status)
	}
	if in.CategoryID > 0 {
		upd.SetCategoryID(in.CategoryID)
	}
	if in.Description != "" {
		upd.SetDescription(in.Description)
	}
	if in.Cover != "" {
		upd.SetCover(in.Cover)
	}
	updated, err := upd.Save(ctx)
	if err != nil {
		return 0, false, err
	}
	return updated.ID, false, nil
}
