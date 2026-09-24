package catalog

// 商品/分类/标签管理 CRUD 数据层（；ent import 收口：data 前缀文件）。
// sanitize 在 service 层调用后传入；slug 唯一校验在 biz。

import (
	"context"
	"fmt"
	placement "github.com/NovaWorks/zcard-next/server/internal/data/ent/categoryproductplacement"
	"strings"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/tag"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	mediamods "github.com/NovaWorks/zcard-next/server/internal/mods/media"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/sanitize"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// ── 商品 ─────────────────────────────────────────────────────

// ListAdmin 管理面商品列表（含下架/隐藏；成本价下发）。
func (r *ProductRepoImpl) ListAdmin(ctx context.Context, f port.AdminFilter) ([]*ent.Product, int64, error) {
	tc := tenancy.FromContext(ctx)
	q := data.Client(ctx, r.data).Product.Query().
		Where(product.SubsiteID(tc.SubsiteID), product.StatusGTE(0)).
		Order(ent.Asc(product.FieldSort), ent.Desc(product.FieldID))
	if f.IsLocked != nil {
		q = q.Where(product.IsLocked(*f.IsLocked))
	}
	if f.CategoryID > 0 {
		// 选择父分类时递归包含全部子分类商品
		desc, err := descendantCategoryIDs(ctx, data.Client(ctx, r.data), f.CategoryID)
		if err != nil {
			return nil, 0, err
		}
		ids := make([]uint64, 0, len(desc))
		for id := range desc {
			ids = append(ids, id)
		}
		q = q.Where(product.CategoryIDIn(ids...))
	}
	if f.StockType != "" {
		if f.StockType != "card" && f.StockType != "url" && f.StockType != "code" {
			return nil, 0, fmt.Errorf("商品库存类型无效")
		}
		q = q.Where(product.StockTypeEQ(product.StockType(f.StockType)))
	}
	if f.Keyword != "" {
		q = q.Where(product.NameContains(f.Keyword)) // 与前台一致：包含匹配（搜名称中段词可命中）
	}
	if f.ConnectionID > 0 {
		q = q.Where(product.UpstreamSourceID(f.ConnectionID))
	}
	if f.LocalOnly {
		q = q.Where(product.Or(product.UpstreamSourceIDIsNil(), product.UpstreamSourceID(0)))
	}
	if f.Status != 0 { // 0=全部（proto3 默认值）；1=上架 2=隐藏 -1=仅下架（DB status=0）
		st := f.Status
		if st == -1 {
			st = 0
		}
		q = q.Where(product.Status(st))
	}
	// 先按货源计算库存，再筛选和分页；未知/不限库存不计入缺货。
	if f.LowStockThreshold > 0 || f.OutOfStockOnly {
		candidates, err := q.Clone().Where(product.Status(1)).All(ctx)
		if err != nil {
			return nil, 0, err
		}
		stocks, err := data.ProductStocks(ctx, r.data, candidates)
		if err != nil {
			return nil, 0, err
		}
		var ids []uint64
		for _, p := range candidates {
			n := stocks[p.ID]
			if (f.OutOfStockOnly && n == 0) || (!f.OutOfStockOnly && n >= 0 && n < int64(f.LowStockThreshold)) {
				ids = append(ids, p.ID)
			}
		}
		if len(ids) == 0 {
			return nil, 0, nil
		}
		q = q.Where(product.IDIn(ids...))
	}

	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	if f.Page > 0 && f.PageSize > 0 {
		q = q.Offset((int(f.Page) - 1) * int(f.PageSize)).Limit(int(f.PageSize))
	}
	if f.OptionsOnly {
		q.Select(product.FieldID, product.FieldName, product.FieldCategoryID, product.FieldPrice, product.FieldStockType, product.FieldSort, product.FieldIsLocked, product.FieldLockVersion, product.FieldStatus, product.FieldCover, product.FieldUpstreamSourceID)
	}
	rows, err := q.All(ctx)
	return rows, int64(total), err
}

// StockBatch returns stock from the product's actual fulfillment source.
func (r *ProductRepoImpl) StockBatch(ctx context.Context, productIDs []uint64) (map[uint64]int64, error) {
	snapshots, err := r.StockSnapshotBatch(ctx, productIDs)
	if err != nil {
		return nil, err
	}
	out := map[uint64]int64{}
	for id, snapshot := range snapshots {
		out[id] = snapshot.Available
	}
	return out, nil
}

func (r *ProductRepoImpl) StockSnapshotBatch(ctx context.Context, productIDs []uint64) (map[uint64]port.StockSnapshot, error) {
	return r.stockSnapshotBatch(ctx, productIDs, true)
}

// Admin lists must not wait for external suppliers; display the cached quantity
// and freshness. Stock-only sync and storefront reads continue refreshing it.
func (r *ProductRepoImpl) cachedStockSnapshotBatch(ctx context.Context, productIDs []uint64) (map[uint64]port.StockSnapshot, error) {
	return r.stockSnapshotBatch(ctx, productIDs, false)
}

func (r *ProductRepoImpl) stockSnapshotBatch(ctx context.Context, productIDs []uint64, refresh bool) (map[uint64]port.StockSnapshot, error) {
	out := map[uint64]port.StockSnapshot{}
	for start := 0; start < len(productIDs); start += 500 {
		end := start + 500
		if end > len(productIDs) {
			end = len(productIDs)
		}
		rows, err := data.Client(ctx, r.data).Product.Query().Where(product.IDIn(productIDs[start:end]...), product.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).All(ctx)
		if err != nil {
			return nil, err
		}
		batch, err := data.ProductStockSnapshots(ctx, r.data, rows)
		if err != nil {
			return nil, err
		}
		if refresh {
			r.refreshDisplayStocks(ctx, rows, batch)
		}
		for id, n := range batch {
			out[id] = port.StockSnapshot{Available: n.Available(), Quantity: n.Quantity, CheckedAt: n.CheckedAt, Status: n.Status}
		}
	}
	return out, nil
}

// GetAdmin 管理面商品详情。
func (r *ProductRepoImpl) GetAdmin(ctx context.Context, subsiteID, id uint64) (*ent.Product, error) {
	return data.Client(ctx, r.data).Product.Query().
		Where(product.ID(id), product.SubsiteID(subsiteID), product.StatusGTE(0)).
		Only(ctx)
}

// CreateProduct 创建商品（description 已 sanitize）。
func (r *ProductRepoImpl) createProduct(ctx context.Context, in port.ProductInput) (*ent.Product, error) {
	if err := validateServiceConfig(in.FulfillmentMode, in.ManualStock, false); err != nil {
		return nil, err
	}
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
	if in.FulfillmentMode != "" {
		create.SetFulfillmentMode(in.FulfillmentMode)
	}
	if in.ManualStock != nil {
		create.SetManualStock(*in.ManualStock)
	}
	if in.DeliveryMode != "" {
		create = create.SetDeliveryMode(product.DeliveryMode(in.DeliveryMode))
	}
	create = create.SetStockVisible(in.StockVisible).SetDedup(in.Dedup).SetSort(in.Sort).SetStatus(in.Status).SetIsRecommend(in.IsRecommend)
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
	if in.PointsRequiredSet {
		create.SetPointsRequired(in.PointsRequired)
	}
	return create.Save(ctx)
}

// SetDirectContent 回填直发内容密文（创建后由 service 按真实 productID 加密调用）。
func (r *ProductRepoImpl) SetDirectContent(ctx context.Context, id uint64, ciphered []byte) error {
	return data.Client(ctx, r.data).Product.UpdateOneID(id).
		SetDirectContent(ciphered).Exec(ctx)
}

// UpdateProduct 更新（nil/零值字段不动）。
func (r *ProductRepoImpl) updateProduct(ctx context.Context, id uint64, in port.ProductInput) (*ent.Product, error) {
	if in.FulfillmentMode == "manual" {
		p, err := data.Client(ctx, r.data).Product.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if p.UpstreamSourceID > 0 {
			return nil, fmt.Errorf("上游商品不能改为本地人工交付，请新建本地服务商品")
		}
	}
	q := data.Client(ctx, r.data).Product.UpdateOneID(id).Where(product.StatusGTE(0), product.SubsiteID(tenancy.FromContext(ctx).SubsiteID))
	if in.Name != "" {
		q.SetName(in.Name)
	}
	if in.CategoryID > 0 {
		q.SetCategoryID(in.CategoryID)
	}
	if in.DescriptionSet || in.Description != "" {
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
	if in.StockType != "" {
		q.SetStockType(product.StockType(in.StockType))
		// 保留旧直发密文供历史订单取货；新订单按 stock_type 走卡池。
	}
	if err := validateServiceConfig(in.FulfillmentMode, in.ManualStock, false); err != nil {
		return nil, err
	}
	if in.FulfillmentMode != "" {
		q.SetFulfillmentMode(in.FulfillmentMode)
	}
	if in.ManualStock != nil {
		q.SetManualStock(*in.ManualStock)
	}
	if in.DeliveryMode != "" {
		q.SetDeliveryMode(product.DeliveryMode(in.DeliveryMode))
	}
	if in.DirectContent != nil {
		q.SetDirectContent(in.DirectContent)
	}
	q.SetStockVisible(in.StockVisible)
	q.SetIsRecommend(in.IsRecommend) // PUT 全量语义（含 false=取消推荐）
	if in.Sort >= 0 {
		q.SetSort(in.Sort)
	}
	if in.Status >= 0 {
		q.SetStatus(in.Status)
	}
	if in.PointsRequiredSet {
		q.SetPointsRequired(in.PointsRequired) // 含 0=移出积分商城（PUT 全量语义）
	}
	if err := q.Exec(ctx); err != nil {
		return nil, err
	}
	return r.GetAdmin(ctx, tenancy.FromContext(ctx).SubsiteID, id)
}

// BatchUpdateStatus 批量上下架（ 列表多选；status 1/0/2）。
func (r *ProductRepoImpl) BatchUpdateStatus(ctx context.Context, ids []uint64, status int8) (int, error) {
	existing, err := data.Client(ctx, r.data).Product.Query().Where(product.IDIn(ids...), product.SubsiteID(tenancy.FromContext(ctx).SubsiteID), product.StatusGTE(0)).IDs(ctx)
	if err != nil {
		return 0, err
	}
	if len(existing) == 0 {
		return 0, nil
	}
	n, _, err := r.batchStatus(ctx, existing, status)
	return n, err
}

// DeleteProduct 删除（软外键约束：有卡密时拒删）。
func (r *ProductRepoImpl) deleteProduct(ctx context.Context, id uint64) error {
	// 删除前清理本地采集封面（失败不阻断删除）
	if p, err := data.Client(ctx, r.data).Product.Get(ctx, id); err == nil {
		deleteProductCover(p.Cover)
	}
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
// 指针语义（缺省不变）：icon nil=不变（空串=清除）；hide nil=不变；
// sort nil=不变；parentId nil=不变（0=置顶级；>0=指定父，防环：
// 不能把分类设为自身或自身的后代，否则树成环）。
func (r *ProductRepoImpl) updateCategory(ctx context.Context, id uint64, name string, icon *string, hide *bool, sort *int32, parentID *int64) (*ent.Category, error) {
	client := data.Client(ctx, r.data)
	if _, err := client.Category.Query().Where(category.ID(id), category.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).Only(ctx); err != nil {
		return nil, err
	}
	q := client.Category.UpdateOneID(id)
	if name != "" {
		q.SetName(name)
	}
	if icon != nil {
		q.SetIcon(*icon)
	}
	if hide != nil {
		q.SetHide(*hide)
	}
	if sort != nil && *sort >= 0 {
		q.SetSort(*sort)
	}
	if parentID != nil {
		if *parentID > 0 {
			if uint64(*parentID) == id {
				return nil, fmt.Errorf("catalog.CATEGORY_CANNOT_PARENT_SELF")
			}
			// 防环：新父不能是自身后代
			desc, err := descendantCategoryIDs(ctx, client, id)
			if err != nil {
				return nil, err
			}
			if desc[uint64(*parentID)] {
				return nil, fmt.Errorf("catalog.CATEGORY_CYCLE")
			}
			q.SetParentID(uint64(*parentID))
		} else {
			q.ClearParentID() // 置顶级
		}
	}
	return q.Save(ctx)
}

// descendantCategoryIDs 收集分类的全部子孙 id（含自身）——防环判据。
func descendantCategoryIDs(ctx context.Context, client *ent.Client, id uint64) (map[uint64]bool, error) {
	out := map[uint64]bool{id: true}
	frontier := []uint64{id}
	for len(frontier) > 0 {
		kids, err := client.Category.Query().Where(category.ParentIDIn(frontier...)).IDs(ctx)
		if err != nil {
			return nil, err
		}
		next := make([]uint64, 0, len(kids))
		for _, k := range kids {
			if !out[k] {
				out[k] = true
				next = append(next, k)
			}
		}
		frontier = next
	}
	return out, nil
}

// DeleteCategory 删除（有子分类或有商品时拒删）。
func (r *ProductRepoImpl) deleteCategory(ctx context.Context, id uint64) error {
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

// ReorderCategories 分类排序（拖拽重排）：把 parent_id 层级下全部兄弟按 ids 顺序
// 重排，sort 归一化为 0..n-1；跨层级移动时一并改父级。事务原子提交。
func (r *ProductRepoImpl) ReorderCategories(ctx context.Context, parentID uint64, ids []uint64) error {
	if len(ids) == 0 {
		return nil
	}
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		if err := lockCategoryStructure(ctx, r.data); err != nil {
			return err
		}
		client := data.Client(ctx, r.data)
		tenant := tenancy.FromContext(ctx).SubsiteID
		if parentID > 0 {
			if _, err := client.Category.Query().Where(category.ID(parentID), category.SubsiteID(tenant)).Only(ctx); err != nil {
				return err
			}
		}
		for i, id := range ids {
			row, err := client.Category.Query().Where(category.ID(id), category.SubsiteID(tenant)).Only(ctx)
			if err != nil {
				return err
			}
			if row.ParentID != parentID {
				if err = r.checkCategoryTree(ctx, id, parentID); err != nil {
					return err
				}
				if err = r.guardCategoryProducts(ctx, []uint64{id}); err != nil {
					return err
				}
			}
			upd := client.Category.UpdateOneID(id).SetSort(int32(i))
			if parentID > 0 {
				upd.SetParentID(parentID)
			} else {
				upd.ClearParentID()
			}
			if err = upd.Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
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
		FulfillmentMode: p.FulfillmentMode, ManualStock: &p.ManualStock,
		Id: p.ID, CategoryId: p.CategoryID, Name: p.Name, Slug: p.Slug,
		Description: p.Description, Cover: p.Cover, Images: p.Images,
		CoverProtected: p.CoverProtected, DescriptionProtected: p.DescriptionProtected,
		PriceCents: p.Price, FactoryPriceCents: p.FactoryPrice,
		StockType: string(p.StockType), StockVisible: p.StockVisible,
		DeliveryMode: string(p.DeliveryMode), Dedup: p.Dedup,
		Sort: p.Sort, Status: int32(p.Status),
		UpstreamSourceId: p.UpstreamSourceID, UpstreamProductCode: p.UpstreamProductCode,
		PointsRequired:   p.PointsRequired,
		HasDirectContent: len(p.DirectContent) > 0,
		IsRecommend:      p.IsRecommend,
		IsLocked:         p.IsLocked, LockVersion: p.LockVersion, LockedBy: p.LockedBy, LockedAt: productLockedAt(p),
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

// UpsertUpstreamProduct 货源同步商品 upsert（，supply 模块经 port 消费）。
// 判据：subsite_id + upstream_source_id + upstream_product_code 幂等。
// Price=-1 保持现有价（价格保护由 supply 侧决策后传入）。
func (r *ProductRepoImpl) UpsertUpstreamProduct(ctx context.Context, in port.UpstreamProductInput) (id uint64, created bool, err error) {
	err = data.Tx(ctx, r.data, func(ctx context.Context) error {
		var e error
		id, created, e = r.upsertUpstreamProduct(ctx, in)
		return e
	})
	return
}

func (r *ProductRepoImpl) upsertUpstreamProduct(ctx context.Context, in port.UpstreamProductInput) (uint64, bool, error) {
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

	if err == nil {
		if !existing.IsLocked && existing.Status < 0 {
			// Archived records retain their manual re-import path.
		} else if _, err = data.GuardProductWrite(ctx, r.data, existing.ID); err != nil {
			return 0, false, err
		}
		locked := data.Client(ctx, r.data).Product.Query().Where(product.ID(existing.ID))
		if r.data.Dialect != db.SQLite {
			locked.ForUpdate()
		}
		existing, err = locked.Only(ctx)
		if err != nil {
			return 0, false, err
		}
	}
	in.Description = sanitize.RichHTML(in.Description)
	if ent.IsNotFound(err) {
		// 新建：slug 用上游标识（稳定、幂等）；状态按 auto_onshelf 开关。
		// 候选 slug（base、base-2 … base-21）一次 IN 查询判重——替代逐个
		// Exist 最多 100 次点查（大数量同步的热点）
		slug := slugify(in.UpstreamProductCode)
		if slug == "" {
			slug = fmt.Sprintf("up-%d-%s", in.ConnectionID, in.UpstreamProductCode)
		}
		baseSlug := slug
		candidates := make([]string, 0, 20)
		candidates = append(candidates, baseSlug)
		for i := 2; i <= 21; i++ {
			candidates = append(candidates, fmt.Sprintf("%s-%d", baseSlug, i))
		}
		takenRows, err2 := data.Client(ctx, r.data).Product.Query().
			Where(product.SubsiteID(tc.SubsiteID), product.SlugIn(candidates...)).
			Select(product.FieldSlug).
			Strings(ctx)
		if err2 != nil {
			return 0, false, err2
		}
		taken := make(map[string]bool, len(takenRows))
		for _, s := range takenRows {
			taken[s] = true
		}
		slug = ""
		for _, c := range candidates {
			if !taken[c] {
				slug = c
				break
			}
		}
		if slug == "" {
			// 前 21 个候选全撞（极端）：从 22 起逐个 Exist 兜底
			slug = fmt.Sprintf("%s-%d", baseSlug, 22)
			for i := 22; i < 100; i++ {
				exists, err3 := data.Client(ctx, r.data).Product.Query().
					Where(product.SubsiteID(tc.SubsiteID), product.Slug(slug)).Exist(ctx)
				if err3 != nil {
					return 0, false, err3
				}
				if !exists {
					break
				}
				slug = fmt.Sprintf("%s-%d", baseSlug, i+1)
			}
		}
		// 上游不可售（手动发货/预选/停售，in.Status=0）恒下架；
		// 在售且允许自动上架才 1（待定价导入 AutoOnshelf=false → 0）
		status := in.Status
		if in.Status == 1 && !in.AutoOnshelf {
			status = 0
		}
		price := in.Price
		if price < 0 {
			price = 0 // 待定价的新商品没有旧价，不能将“不改价”哨兵存为售价。
		}
		create := data.Client(ctx, r.data).Product.Create().
			SetSubsiteID(tc.SubsiteID).
			SetName(in.Name).
			SetSlug(slug).
			SetPrice(price).
			SetFactoryPrice(max(in.FactoryPrice, 0)).
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
		if err := r.syncUpstreamSkus(ctx, tc.SubsiteID, created.ID, in.SKUs); err != nil {
			return 0, false, err
		}
		if err := data.SyncProductMediaRefs(ctx, r.data, nil, created); err != nil {
			return 0, false, err
		}
		return created.ID, true, nil
	}

	if existing.Status < 0 {
		if !in.ReimportDeleted {
			return 0, false, fmt.Errorf("商品已在本地删除，自动同步不会恢复；如需重新导入，请在导入上游商品中勾选该商品")
		}
		// 只释放上游唯一标识；旧商品及其订单、卡密、SKU 继续保留归档。
		// 释放与新建必须同一事务，失败时仍由归档记录阻止自动同步复活。
		var id uint64
		var created bool
		err := data.Tx(ctx, r.data, func(ctx context.Context) error {
			if err := data.Client(ctx, r.data).Product.UpdateOneID(existing.ID).
				Where(product.StatusLT(0), product.UpstreamSourceID(in.ConnectionID), product.UpstreamProductCode(in.UpstreamProductCode)).
				ClearUpstreamProductCode().Exec(ctx); err != nil {
				return err
			}
			in.ReimportDeleted = false
			var err error
			id, created, err = r.UpsertUpstreamProduct(ctx, in)
			return err
		})
		return id, created, err
	}

	// 更新：名称/描述/封面/分类/状态/成本价；价格按保护语义（-1 不动）
	upd := data.Client(ctx, r.data).Product.UpdateOneID(existing.ID).Where(product.StatusGTE(0)).
		SetName(in.Name).
		SetUpstreamSyncedAt(in.UpstreamSyncedAt)
	if in.FactoryPrice >= 0 {
		upd.SetFactoryPrice(in.FactoryPrice)
	}
	if in.Price >= 0 {
		upd.SetPrice(in.Price)
	}
	// 两个调用方（collect 同步/交互导入）恒发送显式状态：镜像上游可售性
	upd.SetStatus(in.Status)
	if in.CategoryID > 0 {
		upd.SetCategoryID(in.CategoryID)
	} else if in.CategorySet {
		upd.ClearCategoryID()
	}
	if !existing.DescriptionProtected && (in.DescriptionSet || in.Description != "") {
		upd.SetDescription(in.Description)
	}
	// cover 恒设（空 = 清空：上游下架/删图时镜像清空，同时调用方已删本地文件）
	if !existing.CoverProtected {
		upd.SetCover(in.Cover)
	}
	updated, err := upd.Save(ctx)
	if err != nil {
		return 0, false, err
	}
	if err := r.syncUpstreamSkus(ctx, tc.SubsiteID, updated.ID, in.SKUs); err != nil {
		return 0, false, err
	}
	if err := data.SyncProductMediaRefs(ctx, r.data, existing, updated); err != nil {
		return 0, false, err
	}
	return updated.ID, false, nil
}

// syncUpstreamSkus 上游规格组合差量同步到 product_skus（按上游 SKU 标识匹配）：
// 新集合有 → 更新价格/规格/上游标识；没有 → 删除（仅删本同步来源的，
// upstream_sku_id 非空的记录——本地手建 SKU 不受影响）。nil 入参 = 不动。
// 价格 -1 = 价格保护语义：已有 SKU 跳过改价，新增组合不创建。
func (r *ProductRepoImpl) syncUpstreamSkus(ctx context.Context, subsiteID, productID uint64, in []port.UpstreamSKUInput) error {
	if in == nil {
		return nil
	}
	client := data.Client(ctx, r.data)
	existing, err := client.ProductSku.Query().
		Where(productsku.ProductID(productID)).
		All(ctx)
	if err != nil {
		return err
	}
	type wanted struct {
		id, name string
		price    int64
		spec     map[string]string
	}
	want := make(map[string]wanted, len(in))
	for _, s := range in {
		name := s.Name
		if name == "" {
			name = s.Code
		}
		want[s.Code] = wanted{id: s.Code, name: name, price: s.PriceCents, spec: s.SpecValues}
	}
	for _, e := range existing {
		if e.UpstreamSkuID == "" {
			continue
		}
		w, ok := want[e.UpstreamSkuID]
		if !ok {
			// 上游已无此组合：仅删同步来源的 SKU（本地手建 upstream_sku_id 为空）
			if e.UpstreamSkuID != "" {
				if err := client.ProductSku.DeleteOneID(e.ID).Exec(ctx); err != nil {
					return err
				}
			}
			continue
		}
		delete(want, e.UpstreamSkuID)
		upd := client.ProductSku.UpdateOneID(e.ID).
			SetName(w.name).
			SetSpecValues(w.spec).
			SetUpstreamSkuID(w.id)
		if w.price >= 0 { // -1 = 价格保护：SKU 保持现价，仅刷新规格与上游标识
			upd = upd.SetPrice(w.price)
		}
		if err := upd.Exec(ctx); err != nil {
			return err
		}
	}
	creates := make([]*ent.ProductSkuCreate, 0, len(want))
	for _, w := range want {
		if w.price < 0 {
			// 价格保护期上游新增的组合：无安全价格可写，跳过创建，
			// 运营解除保护/强制改价后的下一轮同步补齐
			continue
		}
		creates = append(creates, client.ProductSku.Create().
			SetSubsiteID(subsiteID).
			SetProductID(productID).
			SetName(w.name).
			SetPrice(w.price).
			SetSpecValues(w.spec).
			SetUpstreamSkuID(w.id))
	}
	if len(creates) > 0 {
		return client.ProductSku.CreateBulk(creates...).Exec(ctx)
	}
	return nil
}

// ListSupplyCategories 供货目录分类（port.SupplierCatalog；主站一级分类）。
func (r *ProductRepoImpl) ListSupplyCategories(ctx context.Context) ([]port.SupplyCategory, error) {
	tc := tenancy.FromContext(ctx)
	rows, err := data.Client(ctx, r.data).Category.Query().
		Where(category.SubsiteID(tc.SubsiteID), category.Hide(false)).
		Order(ent.Asc(category.FieldSort), ent.Asc(category.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]port.SupplyCategory, 0, len(rows))
	for _, c := range rows {
		out = append(out, port.SupplyCategory{ID: c.ID, Name: c.Name})
	}
	return out, nil
}

// UpdateUpstreamPrice 仅更新价格（port.UpstreamProductMaintainer；price scope 轻量路径）。
func (r *ProductRepoImpl) UpdateUpstreamPrice(ctx context.Context, connectionID uint64, productCode string, priceCents int64, skus ...port.UpstreamSKUInput) (bool, error) {
	tc := tenancy.FromContext(ctx)
	found := false
	err := data.Tx(ctx, r.data, func(ctx context.Context) error {
		client := data.Client(ctx, r.data)
		p, err := client.Product.Query().Where(product.SubsiteID(tc.SubsiteID), product.UpstreamSourceID(connectionID), product.UpstreamProductCode(productCode), product.StatusGTE(0)).Only(ctx)
		if ent.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := data.GuardProductWrite(ctx, r.data, p.ID); err != nil {
			return err
		}
		update := client.Product.UpdateOneID(p.ID).SetUpstreamSyncedAt(time.Now().UTC())
		if priceCents >= 0 {
			update.SetPrice(priceCents)
		}
		if err := update.Exec(ctx); err != nil {
			return err
		}
		for _, sk := range skus {
			if sk.Code == "" || sk.PriceCents < 0 {
				continue
			}
			if err := client.ProductSku.Update().Where(productsku.ProductID(p.ID), productsku.SubsiteID(tc.SubsiteID), productsku.UpstreamSkuID(sk.Code)).SetPrice(sk.PriceCents).Exec(ctx); err != nil {
				return err
			}
		}
		found = true
		return nil
	})
	return found, err
}

// UpdateUpstreamStatus 仅更新上下架状态（port.UpstreamProductMaintainer；status scope 轻量路径）。
func (r *ProductRepoImpl) UpdateUpstreamStatus(ctx context.Context, connectionID uint64, productCode string, status int8) (bool, error) {
	tc := tenancy.FromContext(ctx)
	n, err := data.Client(ctx, r.data).Product.Update().
		Where(
			product.SubsiteID(tc.SubsiteID),
			product.UpstreamSourceID(connectionID),
			product.UpstreamProductCode(productCode),
			product.StatusGTE(0), product.IsLocked(false),
		).
		SetStatus(status).
		SetUpstreamSyncedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ShelveOffMissing 删除对账：连接下未见商品批量下架（port.UpstreamProductMaintainer）。
// 只动 status!=0 的（已下架不重复计数）；seen 为空切片时全量下架（引擎侧护栏
// 保证仅在权威快照完整时调用）。
func (r *ProductRepoImpl) ShelveOffMissing(ctx context.Context, connectionID uint64, seen []string) (int64, error) {
	tc := tenancy.FromContext(ctx)
	q := data.Client(ctx, r.data).Product.Query().
		Where(
			product.SubsiteID(tc.SubsiteID),
			product.UpstreamSourceID(connectionID),
			product.StatusGT(0), product.IsLocked(false),
		)
	if len(seen) > 0 {
		q = q.Where(product.UpstreamProductCodeNotIn(seen...))
	}
	rows, err := q.Select(product.FieldID, product.FieldCover).All(ctx)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	ids := make([]uint64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	n, err := data.Client(ctx, r.data).Product.Update().
		Where(product.IDIn(ids...), product.IsLocked(false), product.StatusGTE(0), product.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).
		SetStatus(0).
		Save(ctx)
	if err != nil {
		return 0, err
	}
	return int64(n), nil
}

// deleteProductCover 删除本地采集封面文件（/uploads/ 路径；其余忽略）。
func deleteProductCover(cover string) {
	if !strings.HasPrefix(cover, "/uploads/") {
		return
	}
	rel := strings.TrimPrefix(cover, "/uploads/")
	rel = strings.ReplaceAll(rel, "\\", "/")
	_ = mediamods.DeleteLocal(rel)
}

// ListForSupply 供货目录分页（ supplier 消费；管理面语义含下架）。
func (r *ProductRepoImpl) ListForSupply(ctx context.Context, f port.AdminFilter) ([]port.SupplierProduct, int64, error) {
	hidden, err := data.HiddenCategoryIDs(ctx, data.Client(ctx, r.data), tenancy.FromContext(ctx).SubsiteID)
	if err != nil {
		return nil, 0, err
	}
	c := data.Client(ctx, r.data)
	controls, e := c.ProductControl.Query().All(ctx)
	if e != nil {
		return nil, 0, e
	}
	var blocked []uint64
	for _, v := range controls {
		blocked = append(blocked, v.ProductID)
	}
	skus, e := c.ProductSku.Query().Where(productsku.FulfillmentMode("manual")).All(ctx)
	if e != nil {
		return nil, 0, e
	}
	for _, v := range skus {
		blocked = append(blocked, v.ProductID)
	}
	q := data.Client(ctx, r.data).Product.Query().Where(product.StatusGTE(0), data.VisibleProductCategory(hidden)).Where(product.FulfillmentModeNEQ("manual"), product.IDNotIn(blocked...))
	if f.Status >= 0 {
		q = q.Where(product.Status(int8(f.Status)))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(ent.Desc(product.FieldID)).
		Offset((int(f.Page) - 1) * int(f.PageSize)).Limit(int(f.PageSize)).All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]port.SupplierProduct, 0, len(rows))
	for _, row := range rows {
		out = append(out, toSupplierProduct(row))
	}
	return out, int64(total), nil
}

// GetForSupply 供货单品（含下架）。
func (r *ProductRepoImpl) GetForSupply(ctx context.Context, productID uint64) (*port.SupplierProduct, error) {
	row, err := data.Client(ctx, r.data).Product.Get(ctx, productID)
	if err != nil {
		return nil, err
	}
	hidden, err := data.HiddenCategoryIDs(ctx, data.Client(ctx, r.data), row.SubsiteID)
	if err != nil {
		return nil, err
	}
	if data.CategoryHidden(hidden, row.CategoryID) {
		return nil, ErrProductNotFound
	}
	if err := data.RejectServiceProduct(ctx, data.Client(ctx, r.data), row); err != nil {
		return nil, err
	}
	p := toSupplierProduct(row)
	return &p, nil
}

func toSupplierProduct(row *ent.Product) port.SupplierProduct {
	return port.SupplierProduct{
		ID:           row.ID,
		Name:         row.Name,
		Price:        row.Price,
		FactoryPrice: row.FactoryPrice,
		CategoryID:   row.CategoryID,
		Description:  row.Description,
		Cover:        row.Cover,
		Status:       row.Status,
	}
}

func (r *ProductRepoImpl) CreateProduct(ctx context.Context, in port.ProductInput) (out *ent.Product, err error) {
	err = data.Tx(ctx, r.data, func(ctx context.Context) error {
		var e error
		out, e = r.createProduct(ctx, in)
		if e != nil {
			return e
		}
		return data.SyncProductMediaRefs(ctx, r.data, nil, out)
	})
	return
}
func (r *ProductRepoImpl) UpdateProduct(ctx context.Context, id uint64, in port.ProductInput) (out *ent.Product, err error) {
	err = data.Tx(ctx, r.data, func(ctx context.Context) error {
		if _, e := data.GuardProductWrite(ctx, r.data, id); e != nil {
			return e
		}
		old, e := data.Client(ctx, r.data).Product.Get(ctx, id)
		if e != nil {
			return e
		}
		out, e = r.updateProduct(ctx, id, in)
		if e != nil {
			return e
		}
		return data.SyncProductMediaRefs(ctx, r.data, old, out)
	})
	return
}
func (r *ProductRepoImpl) DeleteProduct(ctx context.Context, id uint64) error {
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		if _, e := data.GuardProductWrite(ctx, r.data, id); e != nil {
			return e
		}
		old, e := data.Client(ctx, r.data).Product.Get(ctx, id)
		if e != nil {
			return e
		}
		if e = r.deleteProduct(ctx, id); e != nil {
			return e
		}
		return data.SyncProductMediaRefs(ctx, r.data, old, nil)
	})
}

func (r *ProductRepoImpl) UpdateCategory(ctx context.Context, id uint64, name string, icon *string, hide *bool, sort *int32, parentID *int64) (out *ent.Category, err error) {
	err = data.Tx(ctx, r.data, func(ctx context.Context) error {
		if e := lockCategoryStructure(ctx, r.data); e != nil {
			return e
		}
		if parentID != nil {
			if e := r.guardCategoryProducts(ctx, []uint64{id}); e != nil {
				return e
			}
		}
		var e error
		out, e = r.updateCategory(ctx, id, name, icon, hide, sort, parentID)
		return e
	})
	return
}
func (r *ProductRepoImpl) DeleteCategory(ctx context.Context, id uint64) error {
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		if e := lockCategoryStructure(ctx, r.data); e != nil {
			return e
		}
		if e := r.deleteCategory(ctx, id); e != nil {
			return e
		}
		_, e := data.Client(ctx, r.data).CategoryProductPlacement.Delete().Where(placement.CategoryID(id), placement.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).Exec(ctx)
		return e
	})
}
func (r *ProductRepoImpl) guardCategoryProducts(ctx context.Context, ids []uint64) error {
	c := data.Client(ctx, r.data)
	all := []uint64{}
	for _, id := range ids {
		desc, e := descendantCategoryIDs(ctx, c, id)
		if e != nil {
			return e
		}
		for child := range desc {
			all = append(all, child)
		}
	}
	products, e := c.Product.Query().Where(product.SubsiteID(tenancy.FromContext(ctx).SubsiteID), product.CategoryIDIn(all...), product.StatusGTE(0)).Order(ent.Asc(product.FieldID)).IDs(ctx)
	if e != nil {
		return e
	}
	for _, id := range products {
		if _, e = data.GuardProductWrite(ctx, r.data, id); e != nil {
			return e
		}
	}
	return nil
}
