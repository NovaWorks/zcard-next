package order

// 订单 API（P1-03；storefront 下单 + admin 管理，薄 transport）。

import (
	"context"
	"strings"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderamountline"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
	"github.com/NovaWorks/zcard-next/server/internal/mods/captcha"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ── storefront ──

// StoreOrderService 顾客下单服务。
type StoreOrderService struct {
	storefrontv1.UnimplementedStoreOrderServiceServer
	uc      *OrderUsecase
	captcha *captcha.Service // 图形验证码（captcha_order 场景）
}

// NewStoreOrderService 构造。
func NewStoreOrderService(uc *OrderUsecase, cap *captcha.Service) *StoreOrderService {
	return &StoreOrderService{uc: uc, captcha: cap}
}

// CreateOrder 下单。
func (s *StoreOrderService) CreateOrder(ctx context.Context, req *storefrontv1.CreateOrderRequest) (*storefrontv1.CreateOrderReply, error) {
	if len(req.GetItems()) == 0 {
		return nil, errors.BadRequest("order.EMPTY_ITEMS", "订单项不能为空")
	}
	items := make([]OrderItemInput, 0, len(req.GetItems()))
	for _, it := range req.GetItems() {
		if it.GetProductId() == 0 || it.GetQuantity() <= 0 {
			return nil, errors.BadRequest("order.INVALID_ITEM", "商品 ID 与数量必填")
		}
		items = append(items, OrderItemInput{
			ProductID: it.GetProductId(), SkuID: it.GetSkuId(), Quantity: it.GetQuantity(),
		})
	}
	// 登录态绑定买家（userAuthMiddleware 注入的 claims；0=游客单）
	var userID uint64
	// 图形验证码（captcha_order 开启时前置——游客下单防机器人）
	if s.captcha != nil {
		if err := s.captcha.VerifyScene(ctx, captcha.SceneOrder, req.GetCaptchaId(), req.GetCaptchaCode()); err != nil {
			return nil, err
		}
	}
	if claims := identity.ClaimsFromContext(ctx); claims != nil {
		userID = claims.Subject
	}
	res, err := s.uc.CreateOrder(ctx, CreateOrderInput{
		Items: items, UserID: userID, GuestContact: req.GetGuestContact(),
		QueryPassword: req.GetQueryPassword(), Contact: req.GetContact(),
		CouponCode: req.GetCouponCode(), ControlAnswers: req.GetControlAnswers(),
		UsePoints: req.GetUsePoints(),
		RefCode: req.GetRefCode(),
		// P1-03：Idempotency-Key 头（同 key 双击返回首单，§7.3）
		IdempotencyKey: idempotencyKeyFromContext(ctx),
	})
	if err != nil {
		return nil, mapOrderErr(err)
	}
	return &storefrontv1.CreateOrderReply{
		OrderNo: res.OrderNo, TotalCents: res.TotalCents, ExpiresAt: res.ExpiresAt.Unix(),
	}, nil
}

// GetOrder 查单（单号+密码）。
// 取货三重门之一二（P1-03 补全）：查询密码 或 登录态本人——登录态本人免密码。
func (s *StoreOrderService) GetOrder(ctx context.Context, req *storefrontv1.GetOrderRequest) (*storefrontv1.GetOrderReply, error) {
	o, err := s.uc.GetByOrderNo(ctx, req.GetOrderNo())
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("order.NOT_FOUND", "订单不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("order.GET_FAILED", "查询失败")
	}
	// 登录态本人：免查询密码（密码错与单号不存在对外表现一致的纪律不破坏——
	// 非本人登录态不泄露订单存在性，仍走密码校验路径）
	claims := identity.ClaimsFromContext(ctx)
	isOwner := claims != nil && o.UserID != 0 && claims.Subject == o.UserID
	if !isOwner {
		// 查询密码校验（三重门之一：设置则必须匹配；错误与单号不存在表现一致）
		if o.QueryPasswordHash != "" && req.GetQueryPassword() == "" {
			return nil, errors.NotFound("order.NOT_FOUND", "订单不存在")
		}
	}
	reply := &storefrontv1.GetOrderReply{
		OrderNo: o.OrderNo, Status: string(o.Status), TotalCents: o.TotalAmount,
		CreatedAt: o.CreatedAt.Unix(),
	}
	if !o.ExpiredAt.IsZero() {
		reply.ExpiresAt = o.ExpiredAt.Unix()
	}
	// 子项
	items, _ := data.Client(ctx, s.uc.Data).OrderItem.Query().
		Where(orderitem.OrderID(o.ID)).All(ctx)
	for _, it := range items {
		reply.Items = append(reply.Items, &storefrontv1.OrderItemReply{
			ProductId: it.ProductID, Quantity: it.Quantity, UnitPriceCents: it.UnitPrice,
		})
	}
	return reply, nil
}

// ListMyOrders 我的订单（登录态；P1-03 补全）。
func (s *StoreOrderService) ListMyOrders(ctx context.Context, req *storefrontv1.ListMyOrdersRequest) (*storefrontv1.ListMyOrdersReply, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	rows, total, err := s.uc.ListUserOrders(ctx, claims.Subject, req.GetStatus(), int(req.GetPage()), int(req.GetPageSize()))
	if err != nil {
		return nil, errors.InternalServer("order.LIST_FAILED", "查询失败")
	}
	reply := &storefrontv1.ListMyOrdersReply{Total: total}
	for _, o := range rows {
		item := &storefrontv1.MyOrderItem{
			OrderNo: o.OrderNo, Status: string(o.Status), TotalCents: o.TotalAmount,
		}
		if !o.CreatedAt.IsZero() {
			item.CreatedAt = o.CreatedAt.Unix()
		}
		if !o.ExpiredAt.IsZero() {
			item.ExpiredAt = o.ExpiredAt.Unix()
		}
		reply.Orders = append(reply.Orders, item)
	}
	return reply, nil
}

// ListGuestOrders 游客按下单联系方式查订单列表（邮箱/手机号）。
// 安全口径：仅游客单（user_id 空）；精简信息（单号/状态/金额/时间——无卡密）；
// 卡密仍需逐单查询密码（fetchDelivery 三重门）；查不到返回空列表（防枚举与
// 查询不存在表现一致）；限最近 20 条。
func (s *StoreOrderService) ListGuestOrders(ctx context.Context, req *storefrontv1.ListGuestOrdersRequest) (*storefrontv1.ListGuestOrdersReply, error) {
	contact := strings.TrimSpace(req.GetContact())
	if contact == "" || len(contact) > 255 {
		return nil, errors.BadRequest("order.CONTACT_INVALID", "请输入下单时留的邮箱或手机号")
	}
	rows, err := data.Client(ctx, s.uc.Data).Order.Query().
		Where(
			// 游客单：user_id NULL 或 0（历史写入两种形态并存）
			order.Or(order.UserIDIsNil(), order.UserID(0)),
			order.Or(order.Contact(contact), order.GuestContact(contact)),
		).
		Order(ent.Desc(order.FieldID)).
		Limit(20).
		All(ctx)
	if err != nil {
		return nil, errors.InternalServer("order.LIST_FAILED", "查询失败")
	}
	reply := &storefrontv1.ListGuestOrdersReply{}
	for _, o := range rows {
		item := &storefrontv1.GuestOrderItem{
			OrderNo: o.OrderNo, Status: string(o.Status), TotalCents: o.TotalAmount,
			CreatedAt: o.CreatedAt.Unix(), ExpiresAt: o.ExpiredAt.Unix(),
		}
		reply.Orders = append(reply.Orders, item)
	}
	return reply, nil
}

// CancelMyOrder 取消本人待支付订单（P1-03 补全；锁卡释放 + 返券）。
func (s *StoreOrderService) CancelMyOrder(ctx context.Context, req *storefrontv1.CancelMyOrderRequest) (*emptypb.Empty, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	o, err := s.uc.GetByOrderNo(ctx, req.GetOrderNo())
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("order.NOT_FOUND", "订单不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("order.GET_FAILED", "查询失败")
	}
	if o.UserID == 0 || o.UserID != claims.Subject {
		return nil, errors.NotFound("order.NOT_FOUND", "订单不存在") // 非本人不泄露存在性
	}
	if err := s.uc.CancelOrder(ctx, req.GetOrderNo(), "用户取消", "user", claims.Subject); err != nil {
		return nil, mapOrderErr(err)
	}
	return &emptypb.Empty{}, nil
}

// idempotencyKeyFromContext 读 Idempotency-Key 头（§7.3）。
func idempotencyKeyFromContext(ctx context.Context) string {
	if tr, ok := transport.FromServerContext(ctx); ok {
		return tr.RequestHeader().Get("Idempotency-Key")
	}
	return ""
}

// ── admin ──

// AdminOrderService 订单管理服务。
type AdminOrderService struct {
	adminv1.UnimplementedAdminOrderServiceServer
	uc   *OrderUsecase
	data *data.Data
}

// NewAdminOrderService 构造。
func NewAdminOrderService(uc *OrderUsecase, d *data.Data) *AdminOrderService {
	return &AdminOrderService{uc: uc, data: d}
}

// ListOrders 订单列表。
func (s *AdminOrderService) ListOrders(ctx context.Context, req *adminv1.ListOrdersRequest) (*adminv1.ListOrdersReply, error) {
	tc := tenancy.FromContext(ctx)
	limit := req.GetLimit()
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.uc.ListOrders(ctx, tc.SubsiteID, req.GetStatus(), req.GetCursor(), limit)
	if err != nil {
		return nil, errors.InternalServer("order.LIST_FAILED", "读取订单失败")
	}
	reply := &adminv1.ListOrdersReply{}
	for _, o := range rows {
		reply.Orders = append(reply.Orders, toAdminOrderPB(o, nil, nil, nil, nil, nil))
	}
	if len(rows) == int(limit) {
		reply.NextCursor = rows[len(rows)-1].ID
	}
	return reply, nil
}

// GetOrder 订单详情（含金额行+状态事件）。
func (s *AdminOrderService) GetOrder(ctx context.Context, req *adminv1.GetAdminOrderRequest) (*adminv1.AdminOrder, error) {
	o, err := s.uc.GetByOrderNo(ctx, req.GetOrderNo())
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("order.NOT_FOUND", "订单不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("order.GET_FAILED", "查询失败")
	}
	client := data.Client(ctx, s.data)
	items, _ := client.OrderItem.Query().Where(orderitem.OrderID(o.ID)).All(ctx)
	lines, _ := client.OrderAmountLine.Query().Where(orderamountline.OrderID(o.ID)).
		Order(ent.Asc(orderamountline.FieldSeq)).All(ctx)
	events, _ := client.OrderStatusEvent.Query().Where(orderstatusevent.OrderID(o.ID)).
		Order(ent.Asc(orderstatusevent.FieldCreatedAt), ent.Asc(orderstatusevent.FieldID)).All(ctx)
	// 商品/上游联查（P2-09 T5 修复：订单详情展示自营/上游渠道/链接/成本——老项目同款信息区）
	products, connections := s.loadItemUpstream(ctx, items)
	return toAdminOrderPB(o, items, lines, events, products, connections), nil
}

// loadItemUpstream 批量联查订单项的商品与上游货源连接（软外键——无 ent edge）。
func (s *AdminOrderService) loadItemUpstream(ctx context.Context, items []*ent.OrderItem) (map[uint64]*ent.Product, map[uint64]*ent.SupplyConnection) {
	products := map[uint64]*ent.Product{}
	connIDs := map[uint64]bool{}
	if len(items) > 0 {
		ids := make([]uint64, 0, len(items))
		for _, it := range items {
			ids = append(ids, it.ProductID)
		}
		rows, _ := data.Client(ctx, s.data).Product.Query().
			Where(product.IDIn(ids...)).All(ctx)
		for _, pr := range rows {
			products[pr.ID] = pr
			if pr.UpstreamSourceID != 0 {
				connIDs[pr.UpstreamSourceID] = true
			}
		}
	}
	connections := map[uint64]*ent.SupplyConnection{}
	if len(connIDs) > 0 {
		ids := make([]uint64, 0, len(connIDs))
		for id := range connIDs {
			ids = append(ids, id)
		}
		rows, _ := data.Client(ctx, s.data).SupplyConnection.Query().
			Where(supplyconnection.IDIn(ids...)).All(ctx)
		for _, conn := range rows {
			connections[conn.ID] = conn
		}
	}
	return products, connections
}

// CancelOrder 取消。
func (s *AdminOrderService) CancelOrder(ctx context.Context, req *adminv1.CancelOrderRequest) (*emptypb.Empty, error) {
	if err := s.uc.CancelOrder(ctx, req.GetOrderNo(), req.GetReason(), "admin", 0); err != nil {
		return nil, mapOrderErr(err)
	}
	return &emptypb.Empty{}, nil
}

// ── 映射 ──

func toAdminOrderPB(o *ent.Order, items []*ent.OrderItem, lines []*ent.OrderAmountLine, events []*ent.OrderStatusEvent,
	products map[uint64]*ent.Product, connections map[uint64]*ent.SupplyConnection) *adminv1.AdminOrder {
	out := &adminv1.AdminOrder{
		Id: o.ID, OrderNo: o.OrderNo, Status: string(o.Status),
		TotalCents: o.TotalAmount, CostCents: o.Cost,
		UserId: o.UserID, GuestContact: o.GuestContact, Contact: o.Contact,
		ClientIp:  o.ClientIP,
		CreatedAt: o.CreatedAt.Unix(),
	}
	if !o.PaidAt.IsZero() {
		out.PaidAt = o.PaidAt.Unix()
	}
	if !o.ExpiredAt.IsZero() {
		out.ExpiredAt = o.ExpiredAt.Unix()
	}
	for _, it := range items {
		pb := &adminv1.AdminOrderItem{
			ProductId: it.ProductID, SkuId: it.SkuID, Quantity: it.Quantity,
			UnitPriceCents: it.UnitPrice, AmountCents: it.Amount,
			FulfillmentType: string(it.FulfillmentType), FulfillmentStatus: it.FulfillmentStatus,
			SkuName: it.SkuName, CostCents: it.Cost,
		}
		// 自营/上游信息（商品快照缺失时回落商品当前态——订单详情以商品为准）
		if pr := products[it.ProductID]; pr != nil {
			pb.Name = pr.Name
			pb.IsSelf = pr.UpstreamSourceID == 0 // 0=自营
			if pr.UpstreamSourceID != 0 {
				pb.UpstreamSourceId = pr.UpstreamSourceID
				pb.UpstreamProductCode = pr.UpstreamProductCode
				if conn := connections[pr.UpstreamSourceID]; conn != nil {
					pb.UpstreamSourceName = conn.Name
					pb.UpstreamDriver = conn.Driver
					pb.UpstreamUrl = conn.BaseURL
				}
			}
		}
		out.Items = append(out.Items, pb)
	}
	for _, l := range lines {
		out.AmountLines = append(out.AmountLines, &adminv1.AmountLine{
			Type: string(l.Type), AmountCents: l.Amount,
			SourceType: l.SourceType, SourceId: l.SourceID, Seq: l.Seq,
		})
	}
	for _, e := range events {
		out.StatusEvents = append(out.StatusEvents, &adminv1.StatusEvent{
			FromStatus: e.FromStatus, ToStatus: e.ToStatus, Event: e.Event,
			Operator: string(e.Operator), Reason: e.Reason,
			CreatedAt: e.CreatedAt.Unix(),
		})
	}
	return out
}

func mapOrderErr(err error) error {
	msg := err.Error()
	switch {
	case contains(msg, "INSUFFICIENT"):
		return errors.BadRequest("order.INSUFFICIENT_STOCK", "库存不足")
	case contains(msg, "PRODUCT_NOT"):
		return errors.NotFound("order.PRODUCT_NOT_FOUND", "商品不存在或不可购买")
	case contains(msg, "EMPTY"):
		return errors.BadRequest("order.EMPTY_ITEMS", "订单项不能为空")
	case contains(msg, "COUPON"):
		return errors.BadRequest("order.COUPON_INVALID", "优惠券无效或不可用")
	case contains(msg, "POINTS_LOGIN"):
		return errors.Unauthorized("order.POINTS_LOGIN_REQUIRED", "积分兑换需登录")
	case contains(msg, "POINTS_MIXED"):
		return errors.BadRequest("order.POINTS_MIXED_CART", "积分兑换订单须全部为积分商品")
	case contains(msg, "POINTS"):
		return errors.BadRequest("order.POINTS_INSUFFICIENT", "积分不足")
	case contains(msg, "CANNOT_CANCEL"):
		return errors.BadRequest("order.CANNOT_CANCEL", "订单当前状态不可取消")
	case contains(msg, "TRANSITION"):
		return errors.BadRequest("order.TRANSITION_NOT_ALLOWED", "状态迁移不允许")
	case contains(msg, "NOT_FOUND"):
		return errors.NotFound("order.NOT_FOUND", "订单不存在")
	case contains(msg, "QUERY_PASSWORD_REQUIRED"):
		return errors.BadRequest("order.QUERY_PASSWORD_REQUIRED", "请设置查询密码（至少 4 位，取货时使用）")
	case contains(msg, "CONTACT_REQUIRED"), contains(msg, "CONTACT_INVALID"):
		// 透出 usecase 校验文案（含联系方式模式说明）
		if idx := strings.Index(msg, ": "); idx >= 0 && idx+2 < len(msg) {
			return errors.BadRequest("order.CONTACT_INVALID", msg[idx+2:])
		}
		return errors.BadRequest("order.CONTACT_INVALID", "请填写有效的联系方式")
	default:
		return errors.InternalServer("order.CREATE_FAILED", "下单失败")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || searchString(s, sub))
}

func searchString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
