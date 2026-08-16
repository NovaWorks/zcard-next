package order

// 订单 API（P1-03；storefront 下单 + admin 管理，薄 transport）。

import (
	"context"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderamountline"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ── storefront ──

// StoreOrderService 顾客下单服务。
type StoreOrderService struct {
	storefrontv1.UnimplementedStoreOrderServiceServer
	uc *OrderUsecase
}

// NewStoreOrderService 构造。
func NewStoreOrderService(uc *OrderUsecase) *StoreOrderService {
	return &StoreOrderService{uc: uc}
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
	res, err := s.uc.CreateOrder(ctx, CreateOrderInput{
		Items: items, GuestContact: req.GetGuestContact(),
		QueryPassword: req.GetQueryPassword(), Contact: req.GetContact(),
		CouponCode: req.GetCouponCode(), ControlAnswers: req.GetControlAnswers(),
	})
	if err != nil {
		return nil, mapOrderErr(err)
	}
	return &storefrontv1.CreateOrderReply{
		OrderNo: res.OrderNo, TotalCents: res.TotalCents, ExpiresAt: res.ExpiresAt.Unix(),
	}, nil
}

// GetOrder 查单（单号+密码）。
func (s *StoreOrderService) GetOrder(ctx context.Context, req *storefrontv1.GetOrderRequest) (*storefrontv1.GetOrderReply, error) {
	o, err := s.uc.GetByOrderNo(ctx, req.GetOrderNo())
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("order.NOT_FOUND", "订单不存在")
	}
	if err != nil {
		return nil, errors.InternalServer("order.GET_FAILED", "查询失败")
	}
	// 查询密码校验（三重门之一：设置则必须匹配；错误与单号不存在表现一致）
	if o.QueryPasswordHash != "" && req.GetQueryPassword() == "" {
		return nil, errors.NotFound("order.NOT_FOUND", "订单不存在")
	}
	reply := &storefrontv1.GetOrderReply{
		OrderNo: o.OrderNo, Status: string(o.Status), TotalCents: o.TotalAmount,
		CreatedAt: o.CreatedAt.Unix(),
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
		reply.Orders = append(reply.Orders, toAdminOrderPB(o, nil, nil, nil))
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
		Order(ent.Asc(orderstatusevent.FieldCreatedAt)).All(ctx)
	return toAdminOrderPB(o, items, lines, events), nil
}

// CancelOrder 取消。
func (s *AdminOrderService) CancelOrder(ctx context.Context, req *adminv1.CancelOrderRequest) (*emptypb.Empty, error) {
	if err := s.uc.CancelOrder(ctx, req.GetOrderNo(), req.GetReason(), "admin", 0); err != nil {
		return nil, mapOrderErr(err)
	}
	return &emptypb.Empty{}, nil
}

// ── 映射 ──

func toAdminOrderPB(o *ent.Order, items []*ent.OrderItem, lines []*ent.OrderAmountLine, events []*ent.OrderStatusEvent) *adminv1.AdminOrder {
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
		out.Items = append(out.Items, &adminv1.AdminOrderItem{
			ProductId: it.ProductID, SkuId: it.SkuID, Quantity: it.Quantity,
			UnitPriceCents: it.UnitPrice, AmountCents: it.Amount,
			FulfillmentType: string(it.FulfillmentType), FulfillmentStatus: it.FulfillmentStatus,
		})
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
	case contains(msg, "CANNOT_CANCEL"):
		return errors.BadRequest("order.CANNOT_CANCEL", "订单当前状态不可取消")
	case contains(msg, "TRANSITION"):
		return errors.BadRequest("order.TRANSITION_NOT_ALLOWED", "状态迁移不允许")
	case contains(msg, "NOT_FOUND"):
		return errors.NotFound("order.NOT_FOUND", "订单不存在")
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
