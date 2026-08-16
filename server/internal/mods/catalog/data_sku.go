package catalog

// SKU 多规格数据层（M1b）：admin CRUD + 前台列表 + 订单取价（SKU 价 > 商品价）。
// ent import 收口：data 前缀文件（架构测试规则 3b）。

import (
	"context"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// SkuInput SKU 创建/更新输入（price_cents=0 表示继承商品价）。
type SkuInput struct {
	ProductID     uint64
	Name          string
	SpecValues    map[string]string
	PriceCents    int64
	CostCents     int64
	StockOffset   int32
	UpstreamSkuID string
}

// ── admin CRUD ───────────────────────────────────────────────

// ListProductSkus 管理面 SKU 列表。
func (r *ProductRepoImpl) ListProductSkus(ctx context.Context, productID uint64) ([]*ent.ProductSku, error) {
	return data.Client(ctx, r.data).ProductSku.Query().
		Where(productsku.ProductID(productID)).
		Order(ent.Asc(productsku.FieldID)).
		All(ctx)
}

// CreateSku 创建 SKU。
func (r *ProductRepoImpl) CreateSku(ctx context.Context, in SkuInput) (*ent.ProductSku, error) {
	tc := tenancy.FromContext(ctx)
	create := data.Client(ctx, r.data).ProductSku.Create().
		SetSubsiteID(tc.SubsiteID).
		SetProductID(in.ProductID).
		SetName(in.Name).
		SetSpecValues(in.SpecValues).
		SetStockOffset(in.StockOffset)
	if in.PriceCents > 0 {
		create.SetPrice(in.PriceCents)
	}
	if in.CostCents > 0 {
		create.SetCost(in.CostCents)
	}
	if in.UpstreamSkuID != "" {
		create.SetUpstreamSkuID(in.UpstreamSkuID)
	}
	return create.Save(ctx)
}

// UpdateSku 更新 SKU（零值/空字段不动；price_cents 用 >0 判定，0 表示不改）。
func (r *ProductRepoImpl) UpdateSku(ctx context.Context, id uint64, in SkuInput) (*ent.ProductSku, error) {
	q := data.Client(ctx, r.data).ProductSku.UpdateOneID(id)
	if in.Name != "" {
		q.SetName(in.Name)
	}
	if in.SpecValues != nil {
		q.SetSpecValues(in.SpecValues)
	}
	if in.PriceCents > 0 {
		q.SetPrice(in.PriceCents)
	}
	if in.CostCents > 0 {
		q.SetCost(in.CostCents)
	}
	if in.StockOffset != 0 {
		q.SetStockOffset(in.StockOffset)
	}
	if in.UpstreamSkuID != "" {
		q.SetUpstreamSkuID(in.UpstreamSkuID)
	}
	if err := q.Exec(ctx); err != nil {
		return nil, err
	}
	return data.Client(ctx, r.data).ProductSku.Get(ctx, id)
}

// DeleteSku 删除 SKU。
func (r *ProductRepoImpl) DeleteSku(ctx context.Context, id uint64) error {
	n, err := data.Client(ctx, r.data).ProductSku.Delete().Where(productsku.ID(id)).Exec(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("catalog.SKU_NOT_FOUND")
	}
	return nil
}

// ── 前台/订单取价 ────────────────────────────────────────────

// ListSkus 前台 SKU 列表（只下发 id/名称/价格）。
func (r *ProductRepoImpl) ListSkus(ctx context.Context, productID uint64) ([]port.Sku, error) {
	rows, err := data.Client(ctx, r.data).ProductSku.Query().
		Where(productsku.ProductID(productID)).
		Order(ent.Asc(productsku.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]port.Sku, 0, len(rows))
	for _, s := range rows {
		out = append(out, port.Sku{
			ID: s.ID, Name: s.Name, Price: money.Cents(s.Price), ProductID: s.ProductID,
		})
	}
	return out, nil
}

// ResolvePrice 订单取价：SKU 价 > 商品价（SKU 未设独立价或不存在时回落商品价）。
func (r *ProductRepoImpl) ResolvePrice(ctx context.Context, productID, skuID uint64) (money.Cents, error) {
	client := data.Client(ctx, r.data)
	if skuID > 0 {
		sku, err := client.ProductSku.Get(ctx, skuID)
		if err == nil && sku != nil && sku.Price > 0 {
			return money.Cents(sku.Price), nil
		}
	}
	p, err := client.Product.Get(ctx, productID)
	if err != nil {
		return 0, err
	}
	return money.Cents(p.Price), nil
}

var _ port.PricingResolver = (*ProductRepoImpl)(nil)
