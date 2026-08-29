package catalog

// 商品仓储 Ent 实现（租户过滤经 tenancy.Context；M1 交付 Ent interceptor 后
// 本处显式条件作为双保险保留——interceptor 负责注入，业务不依赖「忘了写」）。

import (
	mediaport "github.com/NovaWorks/zcard-next/server/internal/mods/media/port"

	"context"
	"errors"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productcontrol"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// ErrProductNotFound 商品不存在或不可见。
var ErrProductNotFound = errors.New("catalog.PRODUCT_NOT_FOUND")

// ProductRepoImpl 商品仓储实现。
type ProductRepoImpl struct {
	data     *data.Data
	mediaRef mediaport.Referencer // 封面/图集引用计数（nil 跳过）
}

// NewProductRepoImpl 构造（mediaRef 素材引用计数，P3-06）。
func NewProductRepoImpl(d *data.Data, mediaRef mediaport.Referencer) *ProductRepoImpl {
	return &ProductRepoImpl{data: d, mediaRef: mediaRef}
}

// ListVisible 上架商品分页（INDEX(subsite_id, status) 命中；只取列表所需列避免回表）。
func (r *ProductRepoImpl) ListVisible(ctx context.Context, f port.VisibleFilter) ([]port.Product, int64, error) {
	client := data.Client(ctx, r.data)
	q := client.Product.Query().
		Where(
			product.SubsiteID(f.SubsiteID),
			product.Status(1),
		)
	// 排序（sales 由 service 层全量排序，此处不处理）
	switch f.Sort {
	case "price_asc":
		q = q.Order(ent.Asc(product.FieldPrice))
	case "price_desc":
		q = q.Order(ent.Desc(product.FieldPrice))
	case "newest":
		// 最新上架：创建先后降序（区别于综合排序的运营权重 FieldSort 优先）
		q = q.Order(ent.Desc(product.FieldID))
	default:
		// 综合排序（default）：运营权重优先，同权重内新商品在前
		q = q.Order(ent.Asc(product.FieldSort), ent.Desc(product.FieldID))
	}
	if f.CategoryID > 0 {
		// 选择父分类时递归包含全部子分类商品（与后台列表同口径）
		desc, err := descendantCategoryIDs(ctx, client, f.CategoryID)
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
		// 关键词仅等值/前缀匹配（禁止 %xxx% 前缀模糊扫全表，§8.2.5）；全文搜索 M3 走外置
		q = q.Where(product.NameHasPrefix(f.Keyword))
	}
	if f.PointsOnly {
		q = q.Where(product.PointsRequiredGT(0)) // 积分商城视图（P3-01）
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	query := q.Clone()
	// 分页（Page=0 = 全量拉取，sales 排序用）
	if f.Page > 0 && f.PageSize > 0 {
		query = query.Offset((int(f.Page) - 1) * int(f.PageSize)).Limit(int(f.PageSize))
	}
	rows, err := query.All(ctx)
	if err != nil {
		return nil, 0, err
	}
	items := make([]port.Product, 0, len(rows))
	for _, row := range rows {
		items = append(items, toPortProduct(row))
	}
	return items, int64(total), nil
}

// Get 单个商品。
// SkuUpstreamCode 本地 SKU 的上游标识（procurement 规格采购还原；不存在 → 空串）。
func (r *ProductRepoImpl) SkuUpstreamCode(ctx context.Context, subsiteID, skuID uint64) string {
	sku, err := data.Client(ctx, r.data).ProductSku.Get(ctx, skuID)
	if err != nil || sku.SubsiteID != subsiteID {
		return ""
	}
	return sku.UpstreamSkuID
}

func (r *ProductRepoImpl) Get(ctx context.Context, subsiteID, id uint64) (*port.Product, error) {
	row, err := data.Client(ctx, r.data).Product.Query().
		Where(product.ID(id), product.SubsiteID(subsiteID)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrProductNotFound
	}
	if err != nil {
		return nil, err
	}
	p := toPortProduct(row)
	return &p, nil
}

func toPortProduct(row *ent.Product) port.Product {
	return port.Product{
		ID:           row.ID,
		SubsiteID:    row.SubsiteID,
		Name:         row.Name,
		Slug:         row.Slug,
		Cover:        row.Cover,
		Price:        money.Cents(row.Price),
		FactoryPrice: money.Cents(row.FactoryPrice),
		StockType:    string(row.StockType),
		DeliveryMode: string(row.DeliveryMode),
		Status:       row.Status,
		StockVisible: row.StockVisible,
		// P3-01：积分兑换价（积分商城商品判定）
		PointsRequired: row.PointsRequired,
		// P2-02：货源信息（procurement 判定上游项）
		UpstreamSourceID:    row.UpstreamSourceID,
		UpstreamProductCode: row.UpstreamProductCode,
	}
}

// ListVisibleCategories 可见分类（hide=false + 分站白名单；导航/筛选用）。
func (r *ProductRepoImpl) ListVisibleCategories(ctx context.Context, subsiteID uint64) ([]port.Category, error) {
	rows, err := data.Client(ctx, r.data).Category.Query().
		Where(
			category.SubsiteID(subsiteID),
			category.Hide(false),
		).
		Order(ent.Asc(category.FieldSort), ent.Asc(category.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]port.Category, 0, len(rows))
	for _, c := range rows {
		// 分站可见性白名单（空=全部可见；非空且不含本站 → 跳过）
		if len(c.VisibleSubsites) > 0 && !containsUint64(c.VisibleSubsites, subsiteID) {
			continue
		}
		out = append(out, port.Category{ID: c.ID, Name: c.Name, Icon: c.Icon, ParentID: c.ParentID})
	}
	return out, nil
}

func containsUint64(list []uint64, v uint64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ListControls 商品自定义控件（按 sort 升序）。
func (r *ProductRepoImpl) ListControls(ctx context.Context, productID uint64) ([]port.Control, error) {
	rows, err := data.Client(ctx, r.data).ProductControl.Query().
		Where(productcontrol.ProductID(productID)).
		Order(ent.Asc(productcontrol.FieldSort)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]port.Control, 0, len(rows))
	for _, c := range rows {
		out = append(out, port.Control{
			ID: c.ID, Name: c.Name, Type: string(c.Type),
			Required: c.Required, Options: c.Options, Sort: c.Sort,
		})
	}
	return out, nil
}

var _ = tenancy.Main // M1 interceptor 接入后移除占位引用
