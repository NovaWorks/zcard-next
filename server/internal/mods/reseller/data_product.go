package reseller

// 分站自营商品上架链路（ 验收旅程出单段）：
// 分站主自服务面创建商品（subsite_id = 本人 profile.ID）→ 分站域名下单
// → 管线步骤 7 接 ResolveUnitPrice（分站价）→ order.paid → SettleService 利润入账。
// 商品行数据隔离与主站同构：products.subsite_id 行级隔离（铁律 14）。

import (
	"context"
	"fmt"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
)

// OwnProductInput 分站自营商品上架输入（description 已 sanitize）。
type OwnProductInput struct {
	Name         string
	CategoryID   uint64
	Description  string
	Cover        string
	Price        int64 // 分（主站基础价口径；分站价由定价引擎叠加）
	FactoryPrice int64
	StockType    string // card | url | code
	DeliveryMode string
	StockVisible bool
	Sort         int32
	Status       int8 // 1=上架 0=下架 2=隐藏
}

// CreateOwnProduct 分站自营商品创建（subsite_id 显式传入——分站主自服务面，
// 不经租户上下文；slug 在分站内唯一，分站间同 slug 不冲突）。
func (r *ResellerRepo) CreateOwnProduct(ctx context.Context, subsiteID uint64, in OwnProductInput) (*ent.Product, error) {
	client := data.Client(ctx, r.data)
	slug, err := r.ownProductSlug(ctx, subsiteID, in.Name)
	if err != nil {
		return nil, err
	}
	create := client.Product.Create().
		SetSubsiteID(subsiteID).
		SetName(in.Name).
		SetSlug(slug).
		SetPrice(in.Price).
		SetFactoryPrice(in.FactoryPrice)
	if in.StockType != "" {
		create.SetStockType(product.StockType(in.StockType))
	} else {
		create.SetStockType(product.StockTypeCard)
	}
	if in.DeliveryMode != "" {
		create.SetDeliveryMode(product.DeliveryMode(in.DeliveryMode))
	}
	create = create.SetStockVisible(in.StockVisible).SetSort(in.Sort).SetStatus(in.Status)
	if in.CategoryID > 0 {
		create.SetCategoryID(in.CategoryID)
	}
	if in.Description != "" {
		create.SetDescription(in.Description)
	}
	if in.Cover != "" {
		create.SetCover(in.Cover)
	}
	return create.Save(ctx)
}

// ownProductSlug 分站内唯一 slug（与 catalog 同构的 slugify 简化版；
// 判据 products.subsite_id + slug——分站间同名商品不冲突）。
func (r *ResellerRepo) ownProductSlug(ctx context.Context, subsiteID uint64, name string) (string, error) {
	base := slugifyOwn(name)
	if base == "" {
		base = fmt.Sprintf("rs-%d", subsiteID)
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
	return "", fmt.Errorf("reseller.SLUG_EXHAUSTED")
}

func slugifyOwn(s string) string {
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
	return strings.ToLower(string(out))
}
