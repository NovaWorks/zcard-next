package catalog

// 前台商品目录 API（游客可访问；薄 transport：校验 + 装配 + DTO 映射）。

import (
	"context"

	commonv1 "github.com/NovaWorks/zcard-next/server/api/common/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"

	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"

	"github.com/go-kratos/kratos/v3/errors"
)

// StoreCatalogService 前台目录服务（实现 storefrontv1.StoreCatalogService）。
type StoreCatalogService struct {
	storefrontv1.UnimplementedStoreCatalogServiceServer
	uc *CatalogUsecase
}

// NewStoreCatalogService 构造。
func NewStoreCatalogService(uc *CatalogUsecase) *StoreCatalogService {
	return &StoreCatalogService{uc: uc}
}

// ListProducts 商品列表（游客可访问；隐藏商品不出现在列表）。
// 分页参数缺省：page=1 / page_size=20（嵌套 message 的 query 绑定为 page.page=1 形式）。
func (s *StoreCatalogService) ListProducts(ctx context.Context, req *storefrontv1.ListProductsRequest) (*storefrontv1.ListProductsReply, error) {
	tc := tenancy.FromContext(ctx)
	page := req.GetPage().GetPage()
	if page <= 0 {
		page = 1
	}
	pageSize := req.GetPage().GetPageSize()
	if pageSize <= 0 {
		pageSize = 20
	}
	items, total, err := s.uc.ListVisible(ctx, port.VisibleFilter{
		SubsiteID:  tc.SubsiteID,
		CategoryID: req.GetCategoryId(),
		Keyword:    req.GetKeyword(),
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		return nil, errors.InternalServer("catalog.LIST_FAILED", "读取商品列表失败")
	}
	reply := &storefrontv1.ListProductsReply{
		Items: make([]*storefrontv1.Product, 0, len(items)),
		Page: &commonv1.PageResp{
			Total:    total,
			Page:     page,
			PageSize: pageSize,
		},
	}
	for i := range items {
		reply.Items = append(reply.Items, toStorefrontProduct(&items[i]))
	}
	return reply, nil
}

// GetProduct 商品详情（下架/隐藏商品对游客返回 404，不暴露存在性，§5.2 必测项）。
func (s *StoreCatalogService) GetProduct(ctx context.Context, req *storefrontv1.GetProductRequest) (*storefrontv1.Product, error) {
	tc := tenancy.FromContext(ctx)
	p, err := s.uc.GetVisible(ctx, tc.SubsiteID, req.GetId())
	if err != nil {
		return nil, errors.NotFound("catalog.PRODUCT_NOT_FOUND", "商品不存在")
	}
	return toStorefrontProduct(p), nil
}

func toStorefrontProduct(p *port.Product) *storefrontv1.Product {
	return &storefrontv1.Product{
		Id:           p.ID,
		Name:         p.Name,
		Slug:         p.Slug,
		Cover:        "", // cover 字段 M1 加入 Product DTO（当前 port 未含）
		PriceCents:   int64(p.Price),
		StockType:    p.StockType,
		Stock:        0, // 库存数走 inventory 聚合（M1：stock_visible 时返回真实值）
		StockVisible: p.StockVisible,
	}
}
