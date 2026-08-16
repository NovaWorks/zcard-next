// Package catalog 商品目录模块（M1）：商品/SKU/分类/标签/控件/虚拟数据/会员商品组。
//
// M0 骨架打通「租户上下文 → Ent 查询 → 薄 service」链路；价格计算管线
// （PriceCalculator，§5.2）属 order 模块，catalog 只提供基础价与库存类型。
package catalog

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
)

// ProductRepo 商品仓储（模块内端口，实现于 data.go）。
type ProductRepo interface {
	ListVisible(ctx context.Context, f port.VisibleFilter) ([]port.Product, int64, error)
	Get(ctx context.Context, subsiteID, id uint64) (*port.Product, error)
	ListControls(ctx context.Context, productID uint64) ([]port.Control, error)
	ListProductReviews(ctx context.Context, productID uint64) ([]port.ReviewItem, error)
	ListSkus(ctx context.Context, productID uint64) ([]port.Sku, error)
}

// CatalogUsecase 目录用例。
type CatalogUsecase struct {
	repo ProductRepo
}

// NewCatalogUsecase 构造。
func NewCatalogUsecase(repo ProductRepo) *CatalogUsecase { return &CatalogUsecase{repo: repo} }

// ListVisible 顾客可见商品列表（上架 + 租户过滤）。
func (uc *CatalogUsecase) ListVisible(ctx context.Context, f port.VisibleFilter) ([]port.Product, int64, error) {
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.PageSize <= 0 || f.PageSize > 100 {
		f.PageSize = 20
	}
	return uc.repo.ListVisible(ctx, f)
}

// GetVisible 商品详情；下架/隐藏商品对顾客返回 NOT_FOUND（§5.2 必测项）。
func (uc *CatalogUsecase) GetVisible(ctx context.Context, subsiteID, id uint64) (*port.Product, error) {
	p, err := uc.repo.Get(ctx, subsiteID, id)
	if err != nil {
		return nil, err
	}
	// status: 1=上架（游客+会员可见）；2=隐藏（仅会员可见，M1 接会员上下文后放开）
	if p.Status != 1 {
		return nil, ErrProductNotFound
	}
	return p, nil
}

// ListControls 商品自定义控件（下单表单渲染）。
func (uc *CatalogUsecase) ListControls(ctx context.Context, productID uint64) ([]port.Control, error) {
	return uc.repo.ListControls(ctx, productID)
}

// ListProductReviews 商品评价（真实 approved + 虚拟合并）。
func (uc *CatalogUsecase) ListProductReviews(ctx context.Context, productID uint64) ([]port.ReviewItem, error) {
	return uc.repo.ListProductReviews(ctx, productID)
}

// ListSkus 商品多规格（前台只下发 id/名称/价格）。
func (uc *CatalogUsecase) ListSkus(ctx context.Context, productID uint64) ([]port.Sku, error) {
	return uc.repo.ListSkus(ctx, productID)
}
