package catalog

// 前台商品目录 API（游客可访问；薄 transport：校验 + 装配 + DTO 映射）。

import (
	"context"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	resellerport "github.com/NovaWorks/zcard-next/server/internal/mods/reseller/port"

	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"

	"github.com/go-kratos/kratos/v3/errors"
)

// StoreCatalogService 前台目录服务（实现 storefrontv1.StoreCatalogService）。
type StoreCatalogService struct {
	storefrontv1.UnimplementedStoreCatalogServiceServer
	uc *CatalogUsecase
	// pricer 分站定价（P3-04：listing 与 checkout 共用同一 ResolveUnitPrice——1.x 铁律；
	// nil = 主站直营形态不解析分站价）。
	pricer resellerport.Pricer
}

// NewStoreCatalogService 构造。
func NewStoreCatalogService(uc *CatalogUsecase, pricer resellerport.Pricer) *StoreCatalogService {
	return &StoreCatalogService{uc: uc, pricer: pricer}
}

// ListProducts 商品列表（游客可访问；隐藏商品不出现在列表）。
// 分页参数缺省：page=1 / page_size=20（嵌套 message 的 query 绑定为 page.page=1 形式）。
func (s *StoreCatalogService) ListProducts(ctx context.Context, req *storefrontv1.ListProductsRequest) (*storefrontv1.ListProductsReply, error) {
	tc := tenancy.FromContext(ctx)
	page := req.GetPage()
	if page <= 0 {
		page = 1
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = 20
	}
	items, total, err := s.uc.ListVisible(ctx, port.VisibleFilter{
		SubsiteID:  tc.SubsiteID,
		CategoryID: req.GetCategoryId(),
		Keyword:    req.GetKeyword(),
		Page:       page,
		PageSize:   pageSize,
		PointsOnly: req.GetPointsOnly(), // 积分商城视图（P3-01）
	})
	if err != nil {
		return nil, errors.InternalServer("catalog.LIST_FAILED", "读取商品列表失败")
	}
	reply := &storefrontv1.ListProductsReply{
		Items:    make([]*storefrontv1.Product, 0, len(items)),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}
	for i := range items {
		p := &items[i]
		if tc.SubsiteID != tenancy.MainSubsiteID && s.pricer != nil {
			if sp, err := s.pricer.ResolveUnitPrice(ctx, tc.SubsiteID, p.ID, 0, p.Price); err == nil {
				p.Price = sp // 分站价（下限保护在 ResolveUnitPrice 内）
			}
		}
		reply.Items = append(reply.Items, toStorefrontProduct(p))
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
	// P3-04：分站单价（listing=checkout 同源；SKU 规则优先于商品规则由定价引擎裁定）
	base := p.Price
	if tc.SubsiteID != tenancy.MainSubsiteID && s.pricer != nil {
		if sp, err := s.pricer.ResolveUnitPrice(ctx, tc.SubsiteID, p.ID, 0, base); err == nil {
			p.Price = sp
		}
	}
	out := toStorefrontProduct(p)
	controls, err := s.uc.ListControls(ctx, req.GetId())
	if err != nil {
		return nil, errors.InternalServer("catalog.CONTROL_FAILED", "读取控件失败")
	}
	for _, c := range controls {
		out.Controls = append(out.Controls, &storefrontv1.ProductControl{
			Id: c.ID, Name: c.Name, Type: string(c.Type),
			Required: c.Required, Options: c.Options, Sort: c.Sort,
		})
	}
	reviews, err := s.uc.ListProductReviews(ctx, req.GetId())
	if err != nil {
		return nil, errors.InternalServer("catalog.REVIEW_FAILED", "读取评价失败")
	}
	for _, rv := range reviews {
		item := &storefrontv1.ReviewItem{
			Id: rv.ID, Nickname: rv.Nickname, Content: rv.Content,
			Rating: rv.Rating, IsVirtual: rv.IsVirtual,
		}
		if !rv.CreatedAt.IsZero() {
			item.CreatedAt = rv.CreatedAt.Unix()
		}
		out.Reviews = append(out.Reviews, item)
	}
	skus, err := s.uc.ListSkus(ctx, req.GetId())
	if err != nil {
		return nil, errors.InternalServer("catalog.SKU_FAILED", "读取规格失败")
	}
	for _, sku := range skus {
		skuBase := sku.Price
		if skuBase == 0 {
			skuBase = base // 继承商品基础价
		}
		skuPrice := skuBase
		if tc.SubsiteID != tenancy.MainSubsiteID && s.pricer != nil {
			if sp, err := s.pricer.ResolveUnitPrice(ctx, tc.SubsiteID, p.ID, sku.ID, skuBase); err == nil {
				skuPrice = sp
			}
		}
		out.Skus = append(out.Skus, &storefrontv1.Sku{
			Id: sku.ID, Name: sku.Name, PriceCents: int64(skuPrice),
		})
	}
	return out, nil
}

func toStorefrontProduct(p *port.Product) *storefrontv1.Product {
	return &storefrontv1.Product{
		Id:             p.ID,
		Name:           p.Name,
		Slug:           p.Slug,
		Cover:          "", // cover 字段 M1 加入 Product DTO（当前 port 未含）
		PriceCents:     int64(p.Price),
		StockType:      p.StockType,
		Stock:          0, // 库存数走 inventory 聚合（M1：stock_visible 时返回真实值）
		StockVisible:   p.StockVisible,
		PointsRequired: p.PointsRequired, // 积分商城（P3-01；0=常规商品）
	}
}
