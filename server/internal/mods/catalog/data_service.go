package catalog

// 管理面商品目录 API（P1-01；薄 transport——校验+sanitize+DTO 映射，业务在 data_crud.go）。

import (
	"context"
	"encoding/json"
	"strings"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	inventoryport "github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	orderport "github.com/NovaWorks/zcard-next/server/internal/mods/order/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/NovaWorks/zcard-next/server/internal/platform/sanitize"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminCatalogService 管理面目录服务。
type AdminCatalogService struct {
	adminv1.UnimplementedAdminCatalogServiceServer
	repo     *ProductRepoImpl
	stock    inventoryport.StockBatcher // 批量可用库存（nil 容错降级 0）
	sold     orderport.SoldCounter      // 批量已售数量（nil 容错降级 0）
	settings port.SettingsReader        // 低库存阈值等（通道 A；nil = 默认值）
	cipher   *inventory.CardCipher      // 直发内容加密（url/code 商品；nil = 直发不可用）
}

// NewAdminCatalogService 构造。
func NewAdminCatalogService(repo *ProductRepoImpl, stock inventoryport.StockBatcher, sold orderport.SoldCounter, settings port.SettingsReader, cipher *inventory.CardCipher) *AdminCatalogService {
	return &AdminCatalogService{repo: repo, stock: stock, sold: sold, settings: settings, cipher: cipher}
}

// sealDirect 直发内容加密（AAD 绑定 product+subsite）。
func (s *AdminCatalogService) sealDirect(ctx context.Context, plain string, productID uint64) ([]byte, error) {
	tc := tenancy.FromContext(ctx)
	return s.cipher.Seal(plain, productID, tc.SubsiteID)
}

// lowStockThresholdFor 低库存筛选阈值（未开启筛选返回 0=不过滤）。
func (s *AdminCatalogService) lowStockThresholdFor(ctx context.Context, enabled bool) int {
	if !enabled {
		return 0
	}
	return s.lowStockThreshold(ctx)
}

// lowStockThreshold 库存预警阈值（settings.supply.low_stock_threshold；默认 10）。
func (s *AdminCatalogService) lowStockThreshold(ctx context.Context) int {
	if s.settings == nil {
		return 10
	}
	raw, err := s.settings.GetJSON(ctx, "supply", "low_stock_threshold")
	if err != nil || len(raw) == 0 {
		return 10
	}
	var v int
	if json.Unmarshal(raw, &v) != nil || v < 1 {
		return 10
	}
	return v
}

// fillStats 批量填充库存/已售（列表与详情共用；查询失败降级 0 不阻断列表）。
func (s *AdminCatalogService) fillStats(ctx context.Context, items []*adminv1.AdminProduct) {
	if len(items) == 0 {
		return
	}
	ids := make([]uint64, 0, len(items))
	cardIDs := make([]uint64, 0) // 仅卡密类需要库存（链接/兑换码不入卡池）
	for _, p := range items {
		ids = append(ids, p.Id)
		if p.StockType == "card" {
			cardIDs = append(cardIDs, p.Id)
		}
	}
	var stocks, solds map[uint64]int64
	if s.stock != nil {
		stocks, _ = s.stock.StockBatch(ctx, cardIDs) // 失败降级：留 0
	}
	if s.sold != nil {
		solds, _ = s.sold.SoldBatch(ctx, ids)
	}
	// 代发商品（上游货源）：库存 = 上游库存缓存（mapping.up_stock，同步时写入），
	// 非本地卡池数（代发本地无卡密恒 0——曾误导运营以为缺货）
	var upstreamIDs []uint64
	for _, p := range items {
		if p.UpstreamSourceId > 0 {
			upstreamIDs = append(upstreamIDs, p.Id)
		}
	}
	var upStocks map[uint64]int32
	if len(upstreamIDs) > 0 {
		upStocks = s.repo.UpStockBatch(ctx, upstreamIDs)
	}
	for _, p := range items {
		if p.UpstreamSourceId > 0 {
			if v, ok := upStocks[p.Id]; ok {
				p.Stock = int64(v) // -1 = 上游无限
			} else {
				p.Stock = -1 // 无缓存（未同步过）：视为未知/不限
			}
			p.SoldCount = solds[p.Id]
			continue
		}
		if p.StockType == "card" {
			p.Stock = stocks[p.Id]
		} else {
			p.Stock = -1 // 链接/兑换码类：不限（卡池口径不适用）
		}
		p.SoldCount = solds[p.Id]
	}
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
		CategoryID:        req.GetCategoryId(),
		Keyword:           req.GetKeyword(),
		Status:            int8(req.GetStatus()),
		Page:              page,
		PageSize:          size,
		LowStockThreshold: s.lowStockThresholdFor(ctx, req.GetLowStockOnly()),
		ConnectionID:      req.GetUpstreamSourceId(),
		LocalOnly:         req.GetLocalOnly(),
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
	s.fillStats(ctx, reply.Products)
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
	out := ToAdminPB(p)
	s.fillStats(ctx, []*adminv1.AdminProduct{out})
	return out, nil
}

// CreateProduct 创建商品（description sanitize）。
func (s *AdminCatalogService) CreateProduct(ctx context.Context, req *adminv1.CreateProductRequest) (*adminv1.AdminProduct, error) {
	// 铁律 16：管理面提交金额必须过服务端边界校验（非负 + 上限）——抓包改金额拦截点
	if req.GetName() == "" || !money.ValidCents(req.GetPriceCents()) || !money.ValidCents(req.GetFactoryPriceCents()) {
		return nil, errors.BadRequest("catalog.INVALID_INPUT", "名称必填、价格与成本须为非负且不超上限")
	}
	in := port.ProductInput{
		Name: req.GetName(), CategoryID: req.GetCategoryId(),
		Description: sanitize.HTML(req.GetDescription()),
		Cover:       req.GetCover(), Images: req.GetImages(),
		Price: req.GetPriceCents(), FactoryPrice: req.GetFactoryPriceCents(),
		StockType: req.GetStockType(), DeliveryMode: req.GetDeliveryMode(),
		StockVisible: req.GetStockVisible(), Dedup: req.GetDedup(),
		Sort: req.GetSort(), Status: int8(req.GetStatus()),
		PointsRequired: req.GetPointsRequired(), PointsRequiredSet: true,
		IsRecommend: req.GetIsRecommend(),
	}
	p, err := s.repo.CreateProduct(ctx, in)
	if err != nil {
		return nil, errors.InternalServer("catalog.CREATE_FAILED", "创建失败: "+err.Error())
	}
	// 直发内容（url/code）：AAD 绑定 product_id，须在商品落库拿到 ID 后加密回填
	if req.GetStockType() != "card" && req.GetDirectContent() != "" {
		if s.cipher == nil {
			return nil, errors.InternalServer("catalog.CIPHER_UNAVAILABLE", "直发加密不可用")
		}
		ciphered, serr := s.sealDirect(ctx, req.GetDirectContent(), p.ID)
		if serr != nil {
			return nil, errors.InternalServer("catalog.DIRECT_SEAL_FAILED", "直发内容加密失败")
		}
		if err := s.repo.SetDirectContent(ctx, p.ID, ciphered); err != nil {
			return nil, errors.InternalServer("catalog.DIRECT_SAVE_FAILED", "直发内容保存失败")
		}
		p.DirectContent = ciphered
	}
	// 素材引用：封面 + 图集（新建全为引用）
	s.repo.AdjustCoverRefs(ctx, nil, append([]string{in.Cover}, in.Images...))
	return ToAdminPB(p), nil
}

// UpdateProduct 更新商品。
func (s *AdminCatalogService) UpdateProduct(ctx context.Context, req *adminv1.UpdateProductRequest) (*adminv1.AdminProduct, error) {
	if req.GetPriceCents() > money.MaxCents || req.GetFactoryPriceCents() > money.MaxCents {
		return nil, errors.BadRequest("catalog.INVALID_INPUT", "价格与成本不得超出上限")
	}
	in := port.ProductInput{
		Name: req.GetName(), CategoryID: req.GetCategoryId(),
		Description: sanitize.HTML(req.GetDescription()),
		Cover:       req.GetCover(), Images: req.GetImages(),
		Price: req.GetPriceCents(), FactoryPrice: req.GetFactoryPriceCents(),
		DeliveryMode: req.GetDeliveryMode(),
		StockVisible: req.GetStockVisible(),
		Sort:         req.GetSort(), Status: int8(req.GetStatus()),
		PointsRequired: req.GetPointsRequired(), PointsRequiredSet: true,
		IsRecommend: req.GetIsRecommend(), // PUT 全量语义（含 false=取消推荐）
	}
	old, _ := s.repo.GetAdmin(ctx, tenancy.FromContext(ctx).SubsiteID, req.GetId())
	// 直发内容（url/code）：空=保持不变；非空且商品非卡密类 → 加密更新
	if req.GetDirectContent() != "" {
		if s.cipher == nil {
			return nil, errors.InternalServer("catalog.CIPHER_UNAVAILABLE", "直发加密不可用")
		}
		var stockType string
		if old != nil {
			stockType = string(old.StockType)
		}
		if stockType != "card" {
			ciphered, serr := s.sealDirect(ctx, req.GetDirectContent(), req.GetId())
			if serr != nil {
				return nil, errors.InternalServer("catalog.DIRECT_SEAL_FAILED", "直发内容加密失败")
			}
			in.DirectContent = ciphered
		}
	}
	p, err := s.repo.UpdateProduct(ctx, req.GetId(), in)
	if err != nil {
		return nil, errors.InternalServer("catalog.UPDATE_FAILED", "更新失败")
	}
	// 素材引用 diff：旧集合释放 + 新集合引用
	if old != nil {
		oldSet := append([]string{old.Cover}, old.Images...)
		newSet := append([]string{in.Cover}, in.Images...)
		s.repo.AdjustCoverRefs(ctx, oldSet, newSet)
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

// BatchUpdateProductStatus 批量上下架（列表多选）。
func (s *AdminCatalogService) BatchUpdateProductStatus(ctx context.Context, req *adminv1.BatchUpdateProductStatusRequest) (*adminv1.BatchUpdateProductStatusReply, error) {
	n, err := s.repo.BatchUpdateStatus(ctx, req.GetIds(), int8(req.GetStatus()))
	if err != nil {
		return nil, errors.BadRequest("catalog.BATCH_STATUS_INVALID", err.Error())
	}
	return &adminv1.BatchUpdateProductStatusReply{Updated: int32(n)}, nil
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
	c, err := s.repo.UpdateCategory(ctx, req.GetId(), req.GetName(), req.GetIcon(), req.Hide, req.GetSort(), req.GetParentId())
	if err != nil {
		if strings.Contains(err.Error(), "CATEGORY_CYCLE") {
			return nil, errors.BadRequest("catalog.CATEGORY_CYCLE", "不能把分类移到自身或它的子分类下")
		}
		if strings.Contains(err.Error(), "CATEGORY_CANNOT_PARENT_SELF") {
			return nil, errors.BadRequest("catalog.CATEGORY_CANNOT_PARENT_SELF", "分类不能设为自身的子级")
		}
		return nil, errors.InternalServer("catalog.CATEGORY_UPDATE_FAILED", "更新失败")
	}
	return toCategoryPB(c), nil
}

// ReorderCategories 分类排序（拖拽重排：某层级兄弟按 ids 顺序归一化 sort）。
func (s *AdminCatalogService) ReorderCategories(ctx context.Context, req *adminv1.ReorderCategoriesRequest) (*emptypb.Empty, error) {
	if err := s.repo.ReorderCategories(ctx, req.GetParentId(), req.GetIds()); err != nil {
		if strings.Contains(err.Error(), "CATEGORY_CYCLE") {
			return nil, errors.BadRequest("catalog.CATEGORY_CYCLE", "不能把分类移到自身或它的子分类下")
		}
		return nil, errors.InternalServer("catalog.CATEGORY_REORDER_FAILED", "排序失败")
	}
	return &emptypb.Empty{}, nil
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
