package catalog

// 管理面商品目录 API（P1-01；薄 transport——校验+sanitize+DTO 映射，业务在 data_crud.go）。

import (
	"context"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	commonv1 "github.com/NovaWorks/zcard-next/server/api/common/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/sanitize"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminCatalogService 管理面目录服务。
type AdminCatalogService struct {
	adminv1.UnimplementedAdminCatalogServiceServer
	repo *ProductRepoImpl
}

// NewAdminCatalogService 构造。
func NewAdminCatalogService(repo *ProductRepoImpl) *AdminCatalogService {
	return &AdminCatalogService{repo: repo}
}

// ── 商品 ──

// ListProducts 管理面商品列表。
func (s *AdminCatalogService) ListProducts(ctx context.Context, req *adminv1.ListProductsRequest) (*adminv1.ListProductsReply, error) {
	page := int32(1)
	size := int32(20)
	if req.GetPage().GetPage() > 0 {
		page = req.GetPage().GetPage()
	}
	if req.GetPage().GetPageSize() > 0 {
		size = req.GetPage().GetPageSize()
	}
	rows, total, err := s.repo.ListAdmin(ctx, port.AdminFilter{
		CategoryID: req.GetCategoryId(),
		Keyword:    req.GetKeyword(),
		Status:     int8(req.GetStatus()),
		Page:       page, PageSize: size,
	})
	if err != nil {
		return nil, errors.InternalServer("catalog.LIST_FAILED", "读取商品失败")
	}
	reply := &adminv1.ListProductsReply{Products: make([]*adminv1.AdminProduct, 0, len(rows))}
	for _, p := range rows {
		reply.Products = append(reply.Products, ToAdminPB(p))
	}
	reply.Page = &commonv1.PageResp{Total: total, Page: page, PageSize: size}
	return reply, nil
}

// GetProduct 商品详情。
func (s *AdminCatalogService) GetProduct(ctx context.Context, req *adminv1.GetProductRequest) (*adminv1.AdminProduct, error) {
	p, err := s.repo.GetAdmin(ctx, 0, req.GetId()) // TODO: tenant context
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("catalog.PRODUCT_NOT_FOUND", "商品不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("catalog.GET_FAILED", "读取失败")
	}
	return ToAdminPB(p), nil
}

// CreateProduct 创建商品（description sanitize）。
func (s *AdminCatalogService) CreateProduct(ctx context.Context, req *adminv1.CreateProductRequest) (*adminv1.AdminProduct, error) {
	if req.GetName() == "" || req.GetPriceCents() < 0 {
		return nil, errors.BadRequest("catalog.INVALID_INPUT", "名称必填、价格非负")
	}
	in := port.ProductInput{
		Name: req.GetName(), CategoryID: req.GetCategoryId(),
		Description: sanitize.HTML(req.GetDescription()),
		Cover:       req.GetCover(), Images: req.GetImages(),
		Price: req.GetPriceCents(), FactoryPrice: req.GetFactoryPriceCents(),
		StockType: req.GetStockType(), DeliveryMode: req.GetDeliveryMode(),
		StockVisible: req.GetStockVisible(), Dedup: req.GetDedup(),
		Sort: req.GetSort(), Status: int8(req.GetStatus()),
	}
	p, err := s.repo.CreateProduct(ctx, in)
	if err != nil {
		return nil, errors.InternalServer("catalog.CREATE_FAILED", "创建失败: "+err.Error())
	}
	return ToAdminPB(p), nil
}

// UpdateProduct 更新商品。
func (s *AdminCatalogService) UpdateProduct(ctx context.Context, req *adminv1.UpdateProductRequest) (*adminv1.AdminProduct, error) {
	in := port.ProductInput{
		Name: req.GetName(), CategoryID: req.GetCategoryId(),
		Description: sanitize.HTML(req.GetDescription()),
		Cover:       req.GetCover(), Images: req.GetImages(),
		Price: req.GetPriceCents(), FactoryPrice: req.GetFactoryPriceCents(),
		DeliveryMode: req.GetDeliveryMode(),
		StockVisible: req.GetStockVisible(),
		Sort:         req.GetSort(), Status: int8(req.GetStatus()),
	}
	p, err := s.repo.UpdateProduct(ctx, req.GetId(), in)
	if err != nil {
		return nil, errors.InternalServer("catalog.UPDATE_FAILED", "更新失败")
	}
	return ToAdminPB(p), nil
}

// DeleteProduct 删除商品。
func (s *AdminCatalogService) DeleteProduct(ctx context.Context, req *adminv1.DeleteProductRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteProduct(ctx, req.GetId()); err != nil {
		return nil, errors.InternalServer("catalog.DELETE_FAILED", "删除失败")
	}
	return &emptypb.Empty{}, nil
}

// ── 分类 ──

// ListCategories 分类列表。
func (s *AdminCatalogService) ListCategories(ctx context.Context, _ *emptypb.Empty) (*adminv1.CategoryList, error) {
	rows, err := s.repo.ListCategories(ctx)
	if err != nil {
		return nil, errors.InternalServer("catalog.CATEGORY_LIST_FAILED", "读取分类失败")
	}
	reply := &adminv1.CategoryList{}
	for _, c := range rows {
		reply.Categories = append(reply.Categories, toCategoryPB(c))
	}
	return reply, nil
}

// CreateCategory 创建分类。
func (s *AdminCatalogService) CreateCategory(ctx context.Context, req *adminv1.CreateCategoryRequest) (*adminv1.Category, error) {
	c, err := s.repo.CreateCategory(ctx, req.GetName(), req.GetParentId(), req.GetIcon(), req.GetSort())
	if err != nil {
		return nil, errors.BadRequest("catalog.CATEGORY_CREATE_FAILED", "创建失败（可能存在环）")
	}
	return toCategoryPB(c), nil
}

// UpdateCategory 更新分类。
func (s *AdminCatalogService) UpdateCategory(ctx context.Context, req *adminv1.UpdateCategoryRequest) (*adminv1.Category, error) {
	c, err := s.repo.UpdateCategory(ctx, req.GetId(), req.GetName(), req.GetIcon(), req.GetHide(), req.GetSort())
	if err != nil {
		return nil, errors.InternalServer("catalog.CATEGORY_UPDATE_FAILED", "更新失败")
	}
	return toCategoryPB(c), nil
}

// DeleteCategory 删除分类。
func (s *AdminCatalogService) DeleteCategory(ctx context.Context, req *adminv1.DeleteCategoryRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteCategory(ctx, req.GetId()); err != nil {
		return nil, errors.BadRequest("catalog.CATEGORY_DELETE_FAILED", "删除失败（有子分类或商品）")
	}
	return &emptypb.Empty{}, nil
}

// ── 标签 ──

// ListTags 标签列表。
func (s *AdminCatalogService) ListTags(ctx context.Context, _ *emptypb.Empty) (*adminv1.TagList, error) {
	rows, err := s.repo.ListTags(ctx)
	if err != nil {
		return nil, errors.InternalServer("catalog.TAG_LIST_FAILED", "读取标签失败")
	}
	reply := &adminv1.TagList{}
	for _, t := range rows {
		reply.Tags = append(reply.Tags, &adminv1.Tag{
			Id: t.ID, Name: t.Name, Slug: t.Slug, Icon: t.Icon,
			Color: t.Color, Position: string(t.Position), Hide: t.Hide,
		})
	}
	return reply, nil
}

// CreateTag 创建标签。
func (s *AdminCatalogService) CreateTag(ctx context.Context, req *adminv1.CreateTagRequest) (*adminv1.Tag, error) {
	t, err := s.repo.CreateTag(ctx, req.GetName(), req.GetSlug(), req.GetIcon(), req.GetColor(), req.GetPosition())
	if err != nil {
		return nil, errors.InternalServer("catalog.TAG_CREATE_FAILED", "创建失败（slug 可能重复）")
	}
	return &adminv1.Tag{Id: t.ID, Name: t.Name, Slug: t.Slug}, nil
}

// DeleteTag 删除标签。
func (s *AdminCatalogService) DeleteTag(ctx context.Context, req *adminv1.DeleteTagRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteTag(ctx, req.GetId()); err != nil {
		return nil, errors.NotFound("catalog.TAG_NOT_FOUND", "标签不存在")
	}
	return &emptypb.Empty{}, nil
}

// ── 转换 ──

func toCategoryPB(c *ent.Category) *adminv1.Category {
	return &adminv1.Category{
		Id: c.ID, ParentId: c.ParentID, Name: c.Name, Icon: c.Icon,
		Hide: c.Hide, Sort: c.Sort,
	}
}
