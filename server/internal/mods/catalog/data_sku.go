package catalog

// SKU 多规格数据层（M1b）：admin CRUD + 前台列表 + 订单取价（SKU 价 > 商品价）。
// ent import 收口：data 前缀文件（架构测试规则 3b）。

import (
	"context"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryactivity"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryprize"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// SkuInput SKU 创建/更新输入（price_cents=0 表示继承商品价）。
type SkuInput struct {
	FulfillmentMode                   string
	ProductID                         uint64
	Name                              string
	SpecValues                        map[string]string
	PriceCents                        int64
	CostCents                         int64
	StockOffset                       int32
	UpstreamSkuID                     string
	SetPrice, SetCost, SetStockOffset bool
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
func (r *ProductRepoImpl) createSku(ctx context.Context, in SkuInput) (*ent.ProductSku, error) {
	if err := validateServiceConfig(in.FulfillmentMode, nil, true); err != nil {
		return nil, err
	}
	if in.FulfillmentMode == "manual" {
		p, err := data.Client(ctx, r.data).Product.Get(ctx, in.ProductID)
		if err != nil {
			return nil, err
		}
		if p.UpstreamSourceID > 0 {
			return nil, fmt.Errorf("上游规格不能改为本地人工交付")
		}
	}
	tc := tenancy.FromContext(ctx)
	create := data.Client(ctx, r.data).ProductSku.Create().
		SetSubsiteID(tc.SubsiteID).
		SetProductID(in.ProductID).
		SetName(in.Name).
		SetSpecValues(in.SpecValues).
		SetStockOffset(in.StockOffset)
	if in.FulfillmentMode != "" {
		create.SetFulfillmentMode(in.FulfillmentMode)
	}
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

// UpdateSku applies explicitly supplied zero values (price 0 inherits product price).
func (r *ProductRepoImpl) updateSku(ctx context.Context, id uint64, in SkuInput) (*ent.ProductSku, error) {
	if err := validateServiceConfig(in.FulfillmentMode, nil, true); err != nil {
		return nil, err
	}
	if in.FulfillmentMode == "manual" {
		sku, err := data.Client(ctx, r.data).ProductSku.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		p, err := data.Client(ctx, r.data).Product.Get(ctx, sku.ProductID)
		if err != nil {
			return nil, err
		}
		if p.UpstreamSourceID > 0 {
			return nil, fmt.Errorf("上游规格不能改为本地人工交付")
		}
	}
	q := data.Client(ctx, r.data).ProductSku.UpdateOneID(id)
	if in.FulfillmentMode != "" {
		q.SetFulfillmentMode(in.FulfillmentMode)
	}
	if in.Name != "" {
		q.SetName(in.Name)
	}
	if in.SpecValues != nil {
		q.SetSpecValues(in.SpecValues)
	}
	if in.SetPrice || in.PriceCents > 0 {
		q.SetPrice(in.PriceCents)
	}
	if in.SetCost || in.CostCents > 0 {
		q.SetCost(in.CostCents)
	}
	if in.SetStockOffset || in.StockOffset != 0 {
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
func (r *ProductRepoImpl) deleteSku(ctx context.Context, id uint64) error {
	c := data.Client(ctx, r.data)
	activities, err := c.LotteryActivity.Query().Where(lotteryactivity.Published(true), lotteryactivity.StatusNotIn("ended", "archived")).IDs(ctx)
	if err != nil {
		return err
	}
	linked, err := c.LotteryPrize.Query().Where(lotteryprize.SkuID(id), lotteryprize.Enabled(true), lotteryprize.ActivityIDIn(activities...)).Exist(ctx)
	if err != nil {
		return err
	}
	if linked {
		return fmt.Errorf("该规格用于已发布的抽奖活动，请先结束或归档活动")
	}

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
	p, err := data.Client(ctx, r.data).Product.Get(ctx, productID)
	if err != nil {
		return nil, err
	}
	out := make([]port.Sku, 0, len(rows))
	for _, s := range rows {
		mode := data.FulfillmentMode(p, s)
		stock := int64(-2)
		switch mode {
		case "manual":
			stock, err = data.ManualAvailable(ctx, data.Client(ctx, r.data), p)
		case "auto":
			if p.StockType != "card" {
				stock = -1
			} else {
				n, e := data.Client(ctx, r.data).Card.Query().Where(card.ProductID(p.ID), card.SkuID(s.ID), card.StatusEQ(card.StatusAvailable)).Count(ctx)
				stock = int64(n)
				err = e
			}
		}
		if err != nil {
			return nil, err
		}
		out = append(out, port.Sku{Stock: stock,
			FulfillmentMode: s.FulfillmentMode, ID: s.ID, Name: s.Name, Price: money.Cents(s.Price), ProductID: s.ProductID,
		})
	}
	return out, nil
}

// ResolvePrice validates the SKU belongs to this product; zero price inherits the base price.
func (r *ProductRepoImpl) ResolvePrice(ctx context.Context, productID, skuID uint64) (money.Cents, error) {
	client := data.Client(ctx, r.data)
	if skuID > 0 {
		sku, err := client.ProductSku.Query().Where(productsku.ID(skuID), productsku.ProductID(productID)).Only(ctx)
		if err != nil {
			return 0, fmt.Errorf("catalog.SKU_NOT_FOUND: 规格不存在或不属于该商品: %w", err)
		}
		if sku.Price > 0 {
			return money.Cents(sku.Price), nil
		}
	} else {
		exists, err := client.ProductSku.Query().Where(productsku.ProductID(productID)).Exist(ctx)
		if err != nil {
			return 0, err
		}
		if exists {
			return 0, fmt.Errorf("catalog.SKU_REQUIRED: 请选择商品规格")
		}
	}
	p, err := client.Product.Get(ctx, productID)
	if err != nil {
		return 0, err
	}
	return money.Cents(p.Price), nil
}

var _ port.PricingResolver = (*ProductRepoImpl)(nil)

func validateServiceConfig(mode string, stock *int64, sku bool) error {
	if mode != "" && mode != "auto" && mode != "manual" && !(sku && mode == "follow") {
		return fmt.Errorf("交付方式无效")
	}
	if stock != nil && *stock < -1 {
		return fmt.Errorf("人工可售总量须为-1或非负整数")
	}
	return nil
}

func (r *ProductRepoImpl) CreateSku(ctx context.Context, in SkuInput) (out *ent.ProductSku, err error) {
	err = data.Tx(ctx, r.data, func(ctx context.Context) error {

		if _, e := data.GuardProductWrite(ctx, r.data, in.ProductID); e != nil {
			return e
		}

		var inner error
		out, inner = r.createSku(ctx, in)
		return inner
	})
	return
}

func (r *ProductRepoImpl) UpdateSku(ctx context.Context, id uint64, in SkuInput) (out *ent.ProductSku, err error) {
	err = data.Tx(ctx, r.data, func(ctx context.Context) error {
		row, e := data.Client(ctx, r.data).ProductSku.Get(ctx, id)
		if e != nil {
			return e
		}
		if _, e := data.GuardProductWrite(ctx, r.data, row.ProductID); e != nil {
			return e
		}

		var inner error
		out, inner = r.updateSku(ctx, id, in)
		return inner
	})
	return
}

func (r *ProductRepoImpl) DeleteSku(ctx context.Context, id uint64) error {
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		row, e := data.Client(ctx, r.data).ProductSku.Get(ctx, id)
		if e != nil {
			return e
		}
		if _, e := data.GuardProductWrite(ctx, r.data, row.ProductID); e != nil {
			return e
		}

		return r.deleteSku(ctx, id)
	})
}
