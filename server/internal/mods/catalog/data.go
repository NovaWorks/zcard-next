package catalog

// 商品仓储 Ent 实现（租户过滤经 tenancy.Context；M1 交付 Ent interceptor 后
// 本处显式条件作为双保险保留——interceptor 负责注入，业务不依赖「忘了写」）。

import (
	"context"
	"errors"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
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
	data *data.Data
}

// NewProductRepoImpl 构造。
func NewProductRepoImpl(d *data.Data) *ProductRepoImpl { return &ProductRepoImpl{data: d} }

// ListVisible 上架商品分页（INDEX(subsite_id, status) 命中；只取列表所需列避免回表）。
func (r *ProductRepoImpl) ListVisible(ctx context.Context, f port.VisibleFilter) ([]port.Product, int64, error) {
	client := data.Client(ctx, r.data)
	q := client.Product.Query().
		Where(
			product.SubsiteID(f.SubsiteID),
			product.Status(1),
		).
		Order(ent.Asc(product.FieldSort), ent.Desc(product.FieldID))
	if f.CategoryID > 0 {
		q = q.Where(product.CategoryID(f.CategoryID))
	}
	if f.Keyword != "" {
		// 关键词仅等值/前缀匹配（禁止 %xxx% 前缀模糊扫全表，§8.2.5）；全文搜索 M3 走外置
		q = q.Where(product.NameHasPrefix(f.Keyword))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Clone().
		Offset((int(f.Page) - 1) * int(f.PageSize)).
		Limit(int(f.PageSize)).
		All(ctx)
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
		Price:        money.Cents(row.Price),
		FactoryPrice: money.Cents(row.FactoryPrice),
		StockType:    string(row.StockType),
		DeliveryMode: string(row.DeliveryMode),
		Status:       row.Status,
		StockVisible: row.StockVisible,
	}
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
