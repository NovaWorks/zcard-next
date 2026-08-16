package fulfillment

// 履约 API（P1-06；storefront 取货 + admin 手动发货/列表）。

import (
	"context"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ── Storefront ──

// StoreDeliveryService 顾客取货服务。
type StoreDeliveryService struct {
	storefrontv1.UnimplementedStoreDeliveryServiceServer
	repo *DeliveryRepoImpl
}

// NewStoreDeliveryService 构造。
func NewStoreDeliveryService(repo *DeliveryRepoImpl) *StoreDeliveryService {
	return &StoreDeliveryService{repo: repo}
}

// FetchDelivery 取货（三重门——密码错误与单号不存在响应一致）。
func (s *StoreDeliveryService) FetchDelivery(ctx context.Context, req *storefrontv1.FetchDeliveryRequest) (*storefrontv1.FetchDeliveryReply, error) {
	if req.GetOrderNo() == "" || req.GetQueryPassword() == "" {
		return nil, errors.BadRequest("delivery.INVALID_INPUT", "订单号与查询密码必填")
	}
	res, err := s.repo.FetchDelivery(ctx, req.GetOrderNo(), req.GetQueryPassword(), clientIP(ctx))
	if err != nil {
		return nil, errors.NotFound("order.NOT_FOUND", "订单不存在或密码错误")
	}
	reply := &storefrontv1.FetchDeliveryReply{
		OrderNo: res.OrderNo, Status: res.Status, FetchCount: res.FetchCnt,
	}
	for _, item := range res.Items {
		reply.Items = append(reply.Items, &storefrontv1.DeliveryItem{
			Content: item.Content, Masked: item.Masked,
		})
	}
	return reply, nil
}

// MaskedDeliveries 我的订单掩码列表（登录用户）。
func (s *StoreDeliveryService) MaskedDeliveries(ctx context.Context, req *storefrontv1.MaskedDeliveriesRequest) (*storefrontv1.MaskedDeliveriesReply, error) {
	return &storefrontv1.MaskedDeliveriesReply{}, nil // M1b：接用户订单列表
}

// ── Admin ──

// AdminFulfillmentService 履约管理服务。
type AdminFulfillmentService struct {
	adminv1.UnimplementedAdminFulfillmentServiceServer
	repo *DeliveryRepoImpl
	data *data.Data
}

// NewAdminFulfillmentService 构造。
func NewAdminFulfillmentService(repo *DeliveryRepoImpl, d *data.Data) *AdminFulfillmentService {
	return &AdminFulfillmentService{repo: repo, data: d}
}

// ListPending 待人工发货列表。
func (s *AdminFulfillmentService) ListPending(ctx context.Context, req *adminv1.ListPendingRequest) (*adminv1.ListPendingReply, error) {
	page := int(req.GetPage())
	size := int(req.GetPageSize())
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	rows, err := s.repo.ListPending(ctx, page, size)
	if err != nil {
		return nil, errors.InternalServer("fulfillment.LIST_FAILED", "读取待发货失败")
	}
	reply := &adminv1.ListPendingReply{}
	for _, o := range rows {
		reply.Orders = append(reply.Orders, &adminv1.PendingOrder{
			OrderNo: o.OrderNo, CreatedAt: o.CreatedAt.Unix(),
		})
	}
	return reply, nil
}

// ManualDeliver 手动交付。
func (s *AdminFulfillmentService) ManualDeliver(ctx context.Context, req *adminv1.ManualDeliverRequest) (*emptypb.Empty, error) {
	if req.GetOrderNo() == "" || (req.GetContent() == "" && req.GetLogisticsNo() == "") {
		return nil, errors.BadRequest("fulfillment.INVALID_INPUT", "订单号与交付内容必填")
	}
	claims := identity.ClaimsFromContext(ctx)
	var adminID uint64
	if claims != nil {
		adminID = claims.Subject
	}
	if err := s.repo.ManualDeliver(ctx, req.GetOrderNo(), req.GetContent(), req.GetLogisticsNo(), req.GetRemark(), adminID); err != nil {
		return nil, errors.InternalServer("fulfillment.DELIVER_FAILED", "交付失败: "+err.Error())
	}
	return &emptypb.Empty{}, nil
}

// ListDeliveries 交付记录列表（掩码默认）。
func (s *AdminFulfillmentService) ListDeliveries(ctx context.Context, req *adminv1.ListDeliveriesRequest) (*adminv1.ListDeliveriesReply, error) {
	page := int(req.GetPage())
	size := int(req.GetPageSize())
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	rows, _, err := s.repo.ListDeliveries(ctx, req.GetOrderNo(), page, size)
	if err != nil {
		return nil, errors.InternalServer("fulfillment.LIST_FAILED", "读取交付失败")
	}
	reply := &adminv1.ListDeliveriesReply{}
	for _, d := range rows {
		orderNo := s.getOrderNo(ctx, d.OrderID)
		reply.Deliveries = append(reply.Deliveries, &adminv1.DeliveryRecord{
			Id: d.ID, OrderNo: orderNo, CardId: d.CardID,
			ContentMasked: "****", // 掩码默认（card:view_content 权限才可见完整）
			DeliveredMode: string(d.DeliveredMode), DeliveredBy: d.DeliveredBy,
			FetchCount: d.FetchCount, FetchedIp: d.FetchedIP,
		})
		if !d.DeliveredAt.IsZero() {
			reply.Deliveries[len(reply.Deliveries)-1].DeliveredAt = d.DeliveredAt.Unix()
		}
	}
	return reply, nil
}

func (s *AdminFulfillmentService) getOrderNo(ctx context.Context, orderID uint64) string {
	o, err := data.Client(ctx, s.data).Order.Get(ctx, orderID)
	if err != nil {
		return ""
	}
	return o.OrderNo
}

func clientIP(ctx context.Context) string {
	if tr, ok := transport.FromServerContext(ctx); ok {
		if hc, ok := tr.(khttp.Context); ok {
			return hc.Request().RemoteAddr
		}
	}
	return ""
}
