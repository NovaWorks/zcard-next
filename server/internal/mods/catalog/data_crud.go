package catalog

// 商品/分类/标签管理 CRUD 数据层（P1-01；ent import 收口：data 前缀文件）。
// sanitize 在 service 层调用后传入；slug 唯一校验在 biz。

import (
	"context"
	"fmt"
	"strings"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/media"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/tag"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	mediamods "github.com/NovaWorks/zcard-next/server/internal/mods/media"
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
	if f.Keyword != "" {
		q = q.Where(product.NameContains(f.Keyword)) // 与前台一致：包含匹配（搜名称中段词可命中）
	}
	if f.ConnectionID > 0 {
		q = q.Where(product.UpstreamSourceID(f.ConnectionID))
	}
	if f.LocalOnly {
		q = q.Where(product.UpstreamSourceIDIsNil())
	}
	if f.Status != 0 { // 0=全部（proto3 默认值）；1=上架 2=隐藏 -1=仅下架（DB status=0）
		st := f.Status
		if st == -1 {
			st = 0
		}
		q = q.Where(product.Status(st))
	}
	// 低库存过滤（分页前）：与首页预警同口径——仅上架卡密类商品，可用卡密 < 阈值；
	// 无卡密行视为 0。固定 status=1（下架/隐藏不参与售卖，不预警）。
	if f.LowStockThreshold > 0 {
		var candidateIDs []uint64
		if err := q.Clone().
			Where(product.StockTypeEQ(product.StockTypeCard), product.Status(1)).
			Select(product.FieldID).
			Scan(ctx, &candidateIDs); err != nil {
			return nil, 0, err
		}
		if len(candidateIDs) == 0 {
			return nil, 0, nil
		}
		var counts []struct {
			ProductID uint64 `json:"product_id"`
			Count     int    `json:"count"`
		}
		if err := data.Client(ctx, r.data).Card.Query().
			Where(
				card.ProductIDIn(candidateIDs...),
				card.StatusEQ(card.StatusAvailable),
				card.SubsiteID(tc.SubsiteID),
			).
			GroupBy(card.FieldProductID).
			Aggregate(ent.Count()).
			Scan(ctx, &counts); err != nil {
			return nil, 0, err
		}
		stock := make(map[uint64]int64, len(counts))
		for _, c := range counts {
			stock[c.ProductID] = int64(c.Count)
		}
		lowIDs := make([]uint64, 0, len(candidateIDs))
		for _, id := range candidateIDs {
			if stock[id] < int64(f.LowStockThreshold) {
				lowIDs = append(lowIDs, id)
			}
		}
		if len(lowIDs) == 0 {
			return nil, 0, nil
		}
		q = q.Where(product.IDIn(lowIDs...))
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

// UpStockBatch 上游库存缓存批量查询（代发商品列表库存口径；缺省 -1=未知/无限）。
func (r *ProductRepoImpl) UpStockBatch(ctx context.Context, productIDs []uint64) map[uint64]int32 {
	out := map[uint64]int32{}
	if len(productIDs) == 0 {
		return out
	}
	var rows []struct {
		LocalProductID uint64 `json:"local_product_id"`
		UpStock        int32  `json:"up_stock"`
	}
	if err := data.Client(ctx, r.data).SupplyMapping.Query().
		Where(supplymapping.LocalProductIDIn(productIDs...)).
		Select(supplymapping.FieldLocalProductID, supplymapping.FieldUpStock).
		Scan(ctx, &rows); err != nil {
		return out
	}
	for _, r := range rows {
		out[r.LocalProductID] = r.UpStock
	}
	return out
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

// BatchUpdateStatus 批量上下架（P1-01 T2 列表多选；status 1/0/2）。
func (r *ProductRepoImpl) BatchUpdateStatus(ctx context.Context, ids []uint64, status int8) (int, error) {
	if len(ids) == 0 {
		return 0, fmt.Errorf("catalog.EMPTY_IDS")
	}
	if status != 0 && status != 1 && status != 2 {
		return 0, fmt.Errorf("catalog.STATUS_INVALID")
	}
	n, err := data.Client(ctx, r.data).Product.Update().
		Where(product.IDIn(ids...)).
		SetStatus(status).
		Save(ctx)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// DeleteProduct 删除（软外键约束：有卡密时拒删）。
func (r *ProductRepoImpl) DeleteProduct(ctx context.Context, id uint64) error {
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
func (r *ProductRepoImpl) UpdateCategory(ctx context.Context, id uint64, name string, icon *string, hide *bool, sort *int32, parentID *int64) (*ent.Category, error) {
	client := data.Client(ctx, r.data)
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

// ReorderCategories 分类排序（拖拽重排）：把 parent_id 层级下全部兄弟按 ids 顺序
// 重排，sort 归一化为 0..n-1；跨层级移动时一并改父级。事务原子提交。
func (r *ProductRepoImpl) ReorderCategories(ctx context.Context, parentID uint64, ids []uint64) error {
	if len(ids) == 0 {
		return nil
	}
	client := data.Client(ctx, r.data)
	// 防环：目标父级不能是任一被移动分类自身或其后代
	if parentID > 0 {
		for _, id := range ids {
			desc, err := descendantCategoryIDs(ctx, client, id)
			if err != nil {
				return err
			}
			if desc[parentID] {
				return fmt.Errorf("catalog.CATEGORY_CYCLE")
			}
		}
	}
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i, id := range ids {
		upd := tx.Category.UpdateOneID(id).SetSort(int32(i))
		if parentID > 0 {
			upd = upd.SetParentID(parentID)
		} else {
			upd = upd.ClearParentID() // 置顶级
		}
		if err := upd.Exec(ctx); err != nil {
			return err
		}
	}
	return tx.Commit()
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
		PointsRequired:   p.PointsRequired,
		HasDirectContent: len(p.DirectContent) > 0,
		IsRecommend:      p.IsRecommend,
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
		if err := r.syncUpstreamSkus(ctx, tc.SubsiteID, created.ID, in.SKUs); err != nil {
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
	// 两个调用方（collect 同步/交互导入）恒发送显式状态：镜像上游可售性
	upd.SetStatus(in.Status)
	if in.CategoryID > 0 {
		upd.SetCategoryID(in.CategoryID)
	}
	if in.Description != "" {
		upd.SetDescription(in.Description)
	}
	// cover 恒设（空 = 清空：上游下架/删图时镜像清空，同时调用方已删本地文件）
	upd.SetCover(in.Cover)
	updated, err := upd.Save(ctx)
	if err != nil {
		return 0, false, err
	}
	if err := r.syncUpstreamSkus(ctx, tc.SubsiteID, updated.ID, in.SKUs); err != nil {
		return 0, false, err
	}
	return updated.ID, false, nil
}

// syncUpstreamSkus 上游规格组合差量同步到 product_skus（按 name 唯一键）：
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
		want[name] = wanted{id: s.Code, name: name, price: s.PriceCents, spec: s.SpecValues}
	}
	for _, e := range existing {
		w, ok := want[e.Name]
		if !ok {
			// 上游已无此组合：仅删同步来源的 SKU（本地手建 upstream_sku_id 为空）
			if e.UpstreamSkuID != "" {
				if err := client.ProductSku.DeleteOneID(e.ID).Exec(ctx); err != nil {
					return err
				}
			}
			continue
		}
		delete(want, e.Name)
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
func (r *ProductRepoImpl) UpdateUpstreamPrice(ctx context.Context, connectionID uint64, productCode string, priceCents int64) (bool, error) {
	tc := tenancy.FromContext(ctx)
	n, err := data.Client(ctx, r.data).Product.Update().
		Where(
			product.SubsiteID(tc.SubsiteID),
			product.UpstreamSourceID(connectionID),
			product.UpstreamProductCode(productCode),
		).
		SetPrice(priceCents).
		SetUpstreamSyncedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// UpdateUpstreamStatus 仅更新上下架状态（port.UpstreamProductMaintainer；status scope 轻量路径）。
func (r *ProductRepoImpl) UpdateUpstreamStatus(ctx context.Context, connectionID uint64, productCode string, status int8) (bool, error) {
	tc := tenancy.FromContext(ctx)
	n, err := data.Client(ctx, r.data).Product.Update().
		Where(
			product.SubsiteID(tc.SubsiteID),
			product.UpstreamSourceID(connectionID),
			product.UpstreamProductCode(productCode),
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
			product.StatusNEQ(0),
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
		deleteProductCover(row.Cover) // 上游已消失 → 本地封面同步清理
	}
	n, err := data.Client(ctx, r.data).Product.Update().
		Where(product.IDIn(ids...)).
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

// ListForSupply 供货目录分页（P2-03 supplier 消费；管理面语义含下架）。
func (r *ProductRepoImpl) ListForSupply(ctx context.Context, f port.AdminFilter) ([]port.SupplierProduct, int64, error) {
	q := data.Client(ctx, r.data).Product.Query()
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

// AdjustCoverRefs 封面/图集引用调整（旧集合释放 + 新集合引用；id 从 media URL 解析）。
// 旧 catalog 字段存的是 URL（/uploads/<path>）或外链——本方法只对 /uploads/media/<id> 形态计数；
// 简化口径：调用方传新旧 URL 列表，本方法解析出 media id 做增减。
func (r *ProductRepoImpl) AdjustCoverRefs(ctx context.Context, oldURLs, newURLs []string) {
	if r.mediaRef == nil {
		return
	}
	var oldIDs, newIDs []uint64
	for _, u := range oldURLs {
		if id := r.mediaIDFromPath(ctx, u); id > 0 {
			oldIDs = append(oldIDs, id)
		}
	}
	for _, u := range newURLs {
		if id := r.mediaIDFromPath(ctx, u); id > 0 {
			newIDs = append(newIDs, id)
		}
	}
	_ = r.mediaRef.ReleaseRefs(ctx, oldIDs)
	_ = r.mediaRef.AddRefs(ctx, newIDs)
}

// mediaIDFromURL 由素材 URL 反查 id：需要查库（path → media.id）。
// URL 形态 /uploads/YYYY/MM/name.ext —— media 表按 path 索引查。
func (r *ProductRepoImpl) mediaIDFromPath(ctx context.Context, url string) uint64 {
	trimmed := strings.TrimPrefix(url, "/uploads/")
	if trimmed == url {
		return 0 // 外链/非素材库路径不计
	}
	m, err := data.Client(ctx, r.data).Media.Query().Where(media.PathEQ(trimmed)).Only(ctx)
	if err != nil {
		return 0
	}
	return m.ID
}
