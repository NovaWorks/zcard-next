package catalog

// 管理面商品目录 API（P1-01；薄 transport——校验+sanitize+DTO 映射，业务在 data_crud.go）。

import (
	"context"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
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
	if req.GetPage() > 0 {
		page = req.GetPage()
	}
	if req.GetPageSize() > 0 {
		size = req.GetPageSize()
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
	reply.Total = total
	reply.Page = page
	reply.PageSize = size
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

// ── 自定义控件 ──

// ListControls 控件列表。
func (s *AdminCatalogService) ListControls(ctx context.Context, req *adminv1.ListControlsRequest) (*adminv1.ControlList, error) {
	rows, err := s.repo.ListProductControls(ctx, req.GetProductId())
	if err != nil {
		return nil, errors.InternalServer("catalog.CONTROL_LIST_FAILED", "读取控件失败")
	}
	reply := &adminv1.ControlList{}
	for _, c := range rows {
		reply.Controls = append(reply.Controls, toControlPB(c))
	}
	return reply, nil
}

// CreateControl 创建控件。
func (s *AdminCatalogService) CreateControl(ctx context.Context, req *adminv1.CreateControlRequest) (*adminv1.AdminControl, error) {
	if req.GetProductId() == 0 || req.GetName() == "" || req.GetType() == "" {
		return nil, errors.BadRequest("catalog.CONTROL_INVALID", "商品/名称/类型必填")
	}
	c, err := s.repo.CreateProductControl(ctx, req.GetProductId(), 0, req.GetName(), req.GetType(), req.GetRequired(), req.GetOptions(), req.GetSort())
	if err != nil {
		return nil, errors.InternalServer("catalog.CONTROL_CREATE_FAILED", "创建失败")
	}
	return toControlPB(c), nil
}

// UpdateControl 更新控件。
func (s *AdminCatalogService) UpdateControl(ctx context.Context, req *adminv1.UpdateControlRequest) (*adminv1.AdminControl, error) {
	c, err := s.repo.UpdateProductControl(ctx, req.GetId(), req.GetName(), req.GetType(), req.GetRequired(), req.GetOptions(), req.GetSort())
	if err != nil {
		return nil, errors.NotFound("catalog.CONTROL_NOT_FOUND", "控件不存在")
	}
	return toControlPB(c), nil
}

// DeleteControl 删除控件。
func (s *AdminCatalogService) DeleteControl(ctx context.Context, req *adminv1.DeleteControlRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteProductControl(ctx, req.GetId()); err != nil {
		return nil, errors.NotFound("catalog.CONTROL_NOT_FOUND", "控件不存在")
	}
	return &emptypb.Empty{}, nil
}

// ── 评价 ──

// ListReviews 评价列表。
func (s *AdminCatalogService) ListReviews(ctx context.Context, req *adminv1.ListReviewsRequest) (*adminv1.ListReviewsReply, error) {
	rows, total, err := s.repo.ListReviews(ctx, req.GetStatus(), req.GetPage(), req.GetPageSize())
	if err != nil {
		return nil, errors.InternalServer("catalog.REVIEW_LIST_FAILED", "读取评价失败")
	}
	reply := &adminv1.ListReviewsReply{Total: total}
	for _, v := range rows {
		reply.Reviews = append(reply.Reviews, toReviewPB(v))
	}
	return reply, nil
}

// ApproveReview 审核通过。
func (s *AdminCatalogService) ApproveReview(ctx context.Context, req *adminv1.ApproveReviewRequest) (*adminv1.ReviewItem, error) {
	v, err := s.repo.ApproveReview(ctx, req.GetId())
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("catalog.REVIEW_NOT_FOUND", "评价不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("catalog.REVIEW_APPROVE_FAILED", "审核失败")
	}
	return toReviewPB(v), nil
}

// RejectReview 审核拒绝。
func (s *AdminCatalogService) RejectReview(ctx context.Context, req *adminv1.RejectReviewRequest) (*adminv1.ReviewItem, error) {
	v, err := s.repo.RejectReview(ctx, req.GetId())
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("catalog.REVIEW_NOT_FOUND", "评价不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("catalog.REVIEW_REJECT_FAILED", "审核失败")
	}
	return toReviewPB(v), nil
}

// CreateVirtualReview 创建虚拟评价（content sanitize）。
func (s *AdminCatalogService) CreateVirtualReview(ctx context.Context, req *adminv1.CreateVirtualReviewRequest) (*adminv1.VirtualReviewItem, error) {
	if req.GetProductId() == 0 || req.GetContent() == "" {
		return nil, errors.BadRequest("catalog.VIRTUAL_REVIEW_INVALID", "商品 ID 与内容必填")
	}
	v, err := s.repo.CreateVirtualReview(ctx, req.GetProductId(),
		sanitize.Text(req.GetNickname()), sanitize.HTML(req.GetContent()),
		int8(req.GetRating()), req.GetSort())
	if err != nil {
		return nil, errors.InternalServer("catalog.VIRTUAL_REVIEW_CREATE_FAILED", "创建失败")
	}
	return toVirtualReviewPB(v), nil
}

// ── SKU ──

// ListSkus SKU 列表。
func (s *AdminCatalogService) ListSkus(ctx context.Context, req *adminv1.ListSkusRequest) (*adminv1.SkuList, error) {
	rows, err := s.repo.ListProductSkus(ctx, req.GetProductId())
	if err != nil {
		return nil, errors.InternalServer("catalog.SKU_LIST_FAILED", "读取 SKU 失败")
	}
	reply := &adminv1.SkuList{}
	for _, sku := range rows {
		reply.Skus = append(reply.Skus, toSkuPB(sku))
	}
	return reply, nil
}

// CreateSku 创建 SKU。
func (s *AdminCatalogService) CreateSku(ctx context.Context, req *adminv1.CreateSkuRequest) (*adminv1.Sku, error) {
	if req.GetProductId() == 0 || req.GetName() == "" {
		return nil, errors.BadRequest("catalog.SKU_INVALID", "商品 ID 与规格名必填")
	}
	sku, err := s.repo.CreateSku(ctx, SkuInput{
		ProductID: req.GetProductId(), Name: req.GetName(), SpecValues: req.GetSpecValues(),
		PriceCents: req.GetPriceCents(), CostCents: req.GetCostCents(),
		StockOffset: req.GetStockOffset(), UpstreamSkuID: req.GetUpstreamSkuId(),
	})
	if err != nil {
		return nil, errors.InternalServer("catalog.SKU_CREATE_FAILED", "创建失败（规格名可能重复）")
	}
	return toSkuPB(sku), nil
}

// UpdateSku 更新 SKU。
func (s *AdminCatalogService) UpdateSku(ctx context.Context, req *adminv1.UpdateSkuRequest) (*adminv1.Sku, error) {
	sku, err := s.repo.UpdateSku(ctx, req.GetId(), SkuInput{
		Name: req.GetName(), SpecValues: req.GetSpecValues(),
		PriceCents: req.GetPriceCents(), CostCents: req.GetCostCents(),
		StockOffset: req.GetStockOffset(), UpstreamSkuID: req.GetUpstreamSkuId(),
	})
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("catalog.SKU_NOT_FOUND", "SKU 不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("catalog.SKU_UPDATE_FAILED", "更新失败")
	}
	return toSkuPB(sku), nil
}

// DeleteSku 删除 SKU。
func (s *AdminCatalogService) DeleteSku(ctx context.Context, req *adminv1.DeleteSkuRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteSku(ctx, req.GetId()); err != nil {
		return nil, errors.NotFound("catalog.SKU_NOT_FOUND", "SKU 不存在")
	}
	return &emptypb.Empty{}, nil
}

// ── 会员商品组 ──

// ListMemberGroups 商品组列表。
func (s *AdminCatalogService) ListMemberGroups(ctx context.Context, _ *emptypb.Empty) (*adminv1.MemberGroupList, error) {
	rows, err := s.repo.ListMemberGroups(ctx)
	if err != nil {
		return nil, errors.InternalServer("catalog.GROUP_LIST_FAILED", "读取商品组失败")
	}
	reply := &adminv1.MemberGroupList{}
	for _, g := range rows {
		reply.Groups = append(reply.Groups, toMemberGroupPB(g))
	}
	return reply, nil
}

// CreateMemberGroup 创建商品组。
func (s *AdminCatalogService) CreateMemberGroup(ctx context.Context, req *adminv1.CreateMemberGroupRequest) (*adminv1.MemberGroup, error) {
	if req.GetName() == "" || req.GetDiscount() <= 0 || req.GetDiscount() >= 10000 {
		return nil, errors.BadRequest("catalog.GROUP_INVALID", "名称必填，折扣需在 (0,10000) 万分比区间")
	}
	g, err := s.repo.CreateMemberGroup(ctx, GroupInput{
		Name: req.GetName(), ProductIDs: req.GetProductIds(), Discount: req.GetDiscount(),
		StackMember: req.GetStackMember(), StackCoupon: req.GetStackCoupon(),
		BadgeStyle: req.GetBadgeStyle(),
	})
	if err != nil {
		return nil, errors.InternalServer("catalog.GROUP_CREATE_FAILED", "创建失败")
	}
	return toMemberGroupPB(g), nil
}

// UpdateMemberGroup 更新商品组。
func (s *AdminCatalogService) UpdateMemberGroup(ctx context.Context, req *adminv1.UpdateMemberGroupRequest) (*adminv1.MemberGroup, error) {
	g, err := s.repo.UpdateMemberGroup(ctx, req.GetId(), GroupInput{
		Name: req.GetName(), ProductIDs: req.GetProductIds(), Discount: req.GetDiscount(),
		StackMember: req.GetStackMember(), StackCoupon: req.GetStackCoupon(),
		BadgeStyle: req.GetBadgeStyle(),
	})
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("catalog.GROUP_NOT_FOUND", "商品组不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("catalog.GROUP_UPDATE_FAILED", "更新失败")
	}
	return toMemberGroupPB(g), nil
}

// DeleteMemberGroup 删除商品组。
func (s *AdminCatalogService) DeleteMemberGroup(ctx context.Context, req *adminv1.DeleteMemberGroupRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteMemberGroup(ctx, req.GetId()); err != nil {
		return nil, errors.NotFound("catalog.GROUP_NOT_FOUND", "商品组不存在")
	}
	return &emptypb.Empty{}, nil
}

// ── 转换 ──

func toMemberGroupPB(g *ent.MemberProductGroup) *adminv1.MemberGroup {
	return &adminv1.MemberGroup{
		Id: g.ID, Name: g.Name, ProductIds: g.ProductIds, Discount: g.Discount,
		StackMember: g.StackMember, StackCoupon: g.StackCoupon, BadgeStyle: g.BadgeStyle,
	}
}

func toSkuPB(sku *ent.ProductSku) *adminv1.Sku {
	return &adminv1.Sku{
		Id: sku.ID, ProductId: sku.ProductID, Name: sku.Name,
		SpecValues: sku.SpecValues, PriceCents: sku.Price, CostCents: sku.Cost,
		StockOffset: sku.StockOffset, UpstreamSkuId: sku.UpstreamSkuID,
	}
}

func toReviewPB(v *ent.Review) *adminv1.ReviewItem {
	out := &adminv1.ReviewItem{
		Id: v.ID, ProductId: v.ProductID, UserId: v.UserID, OrderId: v.OrderID,
		Rating: int32(v.Rating), Content: v.Content, Status: string(v.Status),
	}
	if !v.CreatedAt.IsZero() {
		out.CreatedAt = v.CreatedAt.Unix()
	}
	return out
}

func toVirtualReviewPB(v *ent.VirtualReview) *adminv1.VirtualReviewItem {
	out := &adminv1.VirtualReviewItem{
		Id: v.ID, ProductId: v.ProductID, Nickname: v.Nickname,
		Content: v.Content, Rating: int32(v.Rating), Sort: v.Sort,
	}
	if !v.CreatedAt.IsZero() {
		out.CreatedAt = v.CreatedAt.Unix()
	}
	return out
}

func toControlPB(c *ent.ProductControl) *adminv1.AdminControl {
	return &adminv1.AdminControl{
		Id: c.ID, ProductId: c.ProductID, Name: c.Name,
		Type: string(c.Type), Required: c.Required, Options: c.Options, Sort: c.Sort,
	}
}

func toCategoryPB(c *ent.Category) *adminv1.Category {
	return &adminv1.Category{
		Id: c.ID, ParentId: c.ParentID, Name: c.Name, Icon: c.Icon,
		Hide: c.Hide, Sort: c.Sort,
	}
}
