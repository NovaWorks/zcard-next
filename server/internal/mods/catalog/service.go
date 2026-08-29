package catalog

// 前台商品目录 API（游客可访问；薄 transport：校验 + 装配 + DTO 映射）。

import (
	"context"
	"sort"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	resellerport "github.com/NovaWorks/zcard-next/server/internal/mods/reseller/port"

	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	inventoryport "github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	orderport "github.com/NovaWorks/zcard-next/server/internal/mods/order/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// StoreCatalogService 前台目录服务（实现 storefrontv1.StoreCatalogService）。
type StoreCatalogService struct {
	storefrontv1.UnimplementedStoreCatalogServiceServer
	uc *CatalogUsecase
	// pricer 分站定价（P3-04：listing 与 checkout 共用同一 ResolveUnitPrice——1.x 铁律；
	// nil = 主站直营形态不解析分站价）。
	pricer resellerport.Pricer
	// stock 批量可用库存（库存真实值；nil 容错降级 0——同 AdminCatalogService）
	stock inventoryport.StockBatcher
	// sold 批量已售数量（列表展示 + sales 排序；nil 容错降级 0）
	sold orderport.SoldCounter
}

// NewStoreCatalogService 构造。
func NewStoreCatalogService(uc *CatalogUsecase, pricer resellerport.Pricer, stock inventoryport.StockBatcher, sold orderport.SoldCounter) *StoreCatalogService {
	return &StoreCatalogService{uc: uc, pricer: pricer, stock: stock, sold: sold}
}

// ListProducts 商品列表（游客可访问；隐藏商品不出现在列表）。
// 分页参数缺省：page=1 / page_size=20（嵌套 message 的 query 绑定为 page.page=1 形式）。
// 排序：default（综合）/ newest / price_asc / price_desc 走 SQL；sales 全量拉取后按销量内存排序分页。
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
	filter := port.VisibleFilter{
		SubsiteID:  tc.SubsiteID,
		CategoryID: req.GetCategoryId(),
		Keyword:    req.GetKeyword(),
		Page:       page,
		PageSize:   pageSize,
		PointsOnly: req.GetPointsOnly(),
		Sort:       req.GetSort(),
	}
	salesSort := req.GetSort() == "sales"
	var items []port.Product
	var total int64
	if salesSort {
		// sales 排序：全量拉取（上架商品量可控）→ 内存按销量排序 → 手动分页
		all, err := s.uc.ListAllVisible(ctx, filter)
		if err != nil {
			return nil, errors.InternalServer("catalog.LIST_FAILED", "读取商品列表失败")
		}
		items, total = all, int64(len(all))
	} else {
		var err error
		items, total, err = s.uc.ListVisible(ctx, filter)
		if err != nil {
			return nil, errors.InternalServer("catalog.LIST_FAILED", "读取商品列表失败")
		}
	}
	// 批量销量（列表展示 + sales 排序）
	var solds map[uint64]int64
	if s.sold != nil {
		ids := make([]uint64, 0, len(items))
		for i := range items {
			ids = append(ids, items[i].ID)
		}
		solds, _ = s.sold.SoldBatch(ctx, ids)
	}
	if salesSort {
		sort.SliceStable(items, func(i, j int) bool {
			return solds[items[i].ID] > solds[items[j].ID]
		})
		start := int((page - 1) * pageSize)
		end := start + int(pageSize)
		if start > len(items) {
			items = nil
		} else if end > len(items) {
			items = items[start:]
		} else {
			items = items[start:end]
		}
	}
	reply := &storefrontv1.ListProductsReply{
		Items:    make([]*storefrontv1.Product, 0, len(items)),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}
	var cardIDs []uint64
	for i := range items {
		if items[i].StockType == "card" {
			cardIDs = append(cardIDs, items[i].ID)
		}
	}
	var stocks map[uint64]int64
	if s.stock != nil {
		stocks, _ = s.stock.StockBatch(ctx, cardIDs) // 失败降级：留 0
	}
	for i := range items {
		p := &items[i]
		if tc.SubsiteID != tenancy.MainSubsiteID && s.pricer != nil {
			if sp, err := s.pricer.ResolveUnitPrice(ctx, tc.SubsiteID, p.ID, 0, p.Price); err == nil {
				p.Price = sp // 分站价（下限保护在 ResolveUnitPrice 内）
			}
		}
		reply.Items = append(reply.Items, toStorefrontProduct(p, stocks, solds[p.ID]))
	}
	return reply, nil
}

// ListCategories 可见分类列表（hide=false；分站白名单；按 sort 排序）。
func (s *StoreCatalogService) ListCategories(ctx context.Context, _ *emptypb.Empty) (*storefrontv1.ListCategoriesReply, error) {
	rows, err := s.uc.ListVisibleCategories(ctx, tenancy.FromContext(ctx).SubsiteID)
	if err != nil {
		return nil, errors.InternalServer("catalog.CATEGORY_FAILED", "读取分类失败")
	}
	reply := &storefrontv1.ListCategoriesReply{}
	for _, c := range rows {
		reply.Categories = append(reply.Categories, &storefrontv1.CategoryItem{
			Id: c.ID, Name: c.Name, Icon: c.Icon, ParentId: c.ParentID,
		})
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
	var stocks map[uint64]int64
	if s.stock != nil && p.StockType == "card" {
		stocks, _ = s.stock.StockBatch(ctx, []uint64{p.ID})
	}
	out := toStorefrontProduct(p, stocks, 0)
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

// toStorefrontProduct DTO 映射。stocks 为批量可用库存（nil = 未注入/失败降级）：
// card 类商品取真实值（stock_visible=false 时仍返回真实值——展示与否由前端
// 按 stock_visible 决定；P3-09 冒烟修复：此前写死 0）；非 card 类 -1=不限。
// soldCount 为该商品已售数（0 = 未注入/无销量）。
func toStorefrontProduct(p *port.Product, stocks map[uint64]int64, soldCount int64) *storefrontv1.Product {
	var stock int64
	if p.StockType == "card" {
		if stocks != nil {
			stock = stocks[p.ID]
		}
	} else {
		stock = -1 // 链接/兑换码类：不限（卡池口径不适用）
	}
	return &storefrontv1.Product{
		Id:             p.ID,
		Name:           p.Name,
		Slug:           p.Slug,
		Cover:          p.Cover,
		PriceCents:     int64(p.Price),
		StockType:      p.StockType,
		Stock:          stock,
		StockVisible:   p.StockVisible,
		PointsRequired: p.PointsRequired, // 积分商城（P3-01；0=常规商品）
		SalesCount:     soldCount,
	}
}
