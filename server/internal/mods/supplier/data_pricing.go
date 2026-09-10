package supplier

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplierproductprice"
	kerrors "github.com/go-kratos/kratos/v3/errors"
)

// A request uses one account pricing snapshot for both list and detail quotes.
type supplierPricing struct {
	fixed      map[[2]uint64]int64
	categories map[uint64]int32
	parents    map[uint64]uint64
	global     int32
}

func (r *SupplierRepoImpl) LoadPricing(ctx context.Context, accountID uint64) (*supplierPricing, error) {
	rows, err := r.ListPrices(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := &supplierPricing{fixed: map[[2]uint64]int64{}, categories: map[uint64]int32{}, parents: map[uint64]uint64{}}
	for _, row := range rows {
		switch row.Scope {
		case "product":
			out.fixed[[2]uint64{row.ProductID, row.SkuID}] = row.Price
		case "category":
			out.categories[row.CategoryID] = row.DiscountBps
		case "global":
			out.global = row.DiscountBps
		default:
			return nil, fmt.Errorf("supplier.INVALID_PRICING: 未知定价范围")
		}
		if row.Scope != "product" && (row.DiscountBps < 1 || row.DiscountBps > 10000) {
			return nil, fmt.Errorf("supplier.INVALID_PRICING: 折扣配置无效")
		}
	}
	if len(out.categories) > 0 {
		categories, err := data.Client(ctx, r.data).Category.Query().All(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range categories {
			out.parents[c.ID] = c.ParentID
		}
	}
	return out, nil
}
func (p *supplierPricing) Price(productID, skuID, categoryID uint64, base int64) int64 {
	if price := p.fixed[[2]uint64{productID, skuID}]; price > 0 {
		return price
	}
	if skuID != 0 {
		if price := p.fixed[[2]uint64{productID, 0}]; price > 0 {
			return price
		}
	}
	discount := p.global
	seen := map[uint64]bool{}
	for id := categoryID; id > 0 && !seen[id]; id = p.parents[id] {
		seen[id] = true
		if d, ok := p.categories[id]; ok {
			discount = d
			break
		}
	}
	if discount == 0 || base <= 0 {
		return base
	}
	// Integer half-up rounding; splitting first avoids overflow on large base prices.
	price := base/10000*int64(discount) + (base%10000*int64(discount)+5000)/10000
	if price < 1 {
		return 1
	}
	return price
}

func (r *SupplierRepoImpl) UpsertPriceRule(ctx context.Context, accountID, productID, skuID, categoryID uint64, scope string, price int64, discount int32) error {
	invalid := func(message string) error { return kerrors.BadRequest("supplier.INVALID_PRICING", message) }
	if scope == "" {
		scope = "product"
	}
	client := data.Client(ctx, r.data)
	if _, err := client.SupplierAccount.Get(ctx, accountID); err != nil {
		if ent.IsNotFound(err) {
			return invalid("供货账号不存在")
		}
		return err
	}
	switch scope {
	case "product":
		if productID == 0 || price <= 0 || categoryID != 0 || discount != 0 {
			return invalid("请选择商品并填写大于零的专属价")
		}
		if _, err := client.Product.Get(ctx, productID); err != nil {
			if ent.IsNotFound(err) {
				return invalid("商品不存在")
			}
			return err
		}
		if skuID > 0 {
			sku, e := client.ProductSku.Get(ctx, skuID)
			if ent.IsNotFound(e) {
				return invalid("规格不存在")
			}
			if e != nil {
				return e
			}
			if sku.ProductID != productID {
				return invalid("规格不属于所选商品")
			}
		}
	case "category", "global":
		if productID != 0 || skuID != 0 || price != 0 || discount < 1 || discount > 10000 {
			return invalid("折扣须大于 0 且不超过 10 折")
		}
		if scope == "category" {
			if categoryID == 0 {
				return invalid("请选择分类")
			}
			if _, err := client.Category.Get(ctx, categoryID); err != nil {
				if ent.IsNotFound(err) {
					return invalid("分类不存在")
				}
				return err
			}
		} else if categoryID != 0 {
			return invalid("整站规则不能指定分类")
		}
	default:
		return invalid("不支持的定价范围")
	}
	return client.SupplierProductPrice.Create().SetSupplierAccountID(accountID).SetProductID(productID).SetSkuID(skuID).SetCategoryID(categoryID).SetScope(scope).SetPrice(price).SetDiscountBps(discount).
		OnConflictColumns(supplierproductprice.FieldSupplierAccountID, supplierproductprice.FieldScope, supplierproductprice.FieldProductID, supplierproductprice.FieldSkuID, supplierproductprice.FieldCategoryID).UpdateNewValues().Exec(ctx)
}

func (r *SupplierRepoImpl) PriceNames(ctx context.Context, rows []*ent.SupplierProductPrice) (map[uint64][3]string, error) {
	out := map[uint64][3]string{}
	productIDs, categoryIDs, skuIDs := []uint64{}, []uint64{}, []uint64{}
	for _, row := range rows {
		if row.ProductID > 0 {
			productIDs = append(productIDs, row.ProductID)
		}
		if row.CategoryID > 0 {
			categoryIDs = append(categoryIDs, row.CategoryID)
		}
		if row.SkuID > 0 {
			skuIDs = append(skuIDs, row.SkuID)
		}
	}
	products, categories, skus := map[uint64]string{}, map[uint64]string{}, map[uint64]string{}
	c := data.Client(ctx, r.data)
	if len(productIDs) > 0 {
		items, err := c.Product.Query().Where(product.IDIn(productIDs...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			products[it.ID] = it.Name
		}
	}
	if len(categoryIDs) > 0 {
		items, err := c.Category.Query().Where(category.IDIn(categoryIDs...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			categories[it.ID] = it.Name
		}
	}
	if len(skuIDs) > 0 {
		items, err := c.ProductSku.Query().Where(productsku.IDIn(skuIDs...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			skus[it.ID] = it.Name
		}
	}
	for _, row := range rows {
		out[row.ID] = [3]string{products[row.ProductID], categories[row.CategoryID], skus[row.SkuID]}
	}
	return out, nil
}
