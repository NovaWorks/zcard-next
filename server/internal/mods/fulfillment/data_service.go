package fulfillment

// 履约 API（；storefront 取货 + admin 手动发货/列表）。

import (
	"context"
	"fmt"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderdelivery"
	auditport "github.com/NovaWorks/zcard-next/server/internal/mods/audit/port"
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
	return &storefrontv1.MaskedDeliveriesReply{}, nil // ：接用户订单列表
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
	// 商品名批查（item 仅存 product_id 快照）
	ids := make([]uint64, 0, len(rows)*2)
	for _, o := range rows {
		for _, it := range o.Edges.Items {
			ids = append(ids, it.ProductID)
		}
	}
	names, _ := s.repo.ProductNames(ctx, ids)
	reply := &adminv1.ListPendingReply{}
	for _, o := range rows {
		po := &adminv1.PendingOrder{OrderNo: o.OrderNo, CreatedAt: o.CreatedAt.Unix()}
		if items := o.Edges.Items; len(items) > 0 {
			po.ProductId = items[0].ProductID
			name := names[items[0].ProductID]
			if name == "" {
				name = fmt.Sprintf("#%d", items[0].ProductID)
			}
			if len(items) > 1 {
				name = fmt.Sprintf("%s 等 %d 件商品", name, len(items))
			}
			po.ProductName = name
			for _, it := range items {
				po.Quantity += it.Quantity
			}
		}
		reply.Orders = append(reply.Orders, po)
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

// ListDeliveries 交付记录列表（含完整卡密——现场解密不落库；路由受
// order:view_delivery 权限门控，查看行为审计留痕）。
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
	// 明文查看审计（管理端看卡密是敏感操作——谁/何时看了哪单）
	if s.repo.auditor != nil && len(rows) > 0 {
		adminID := uint64(0)
		if claims := identity.ClaimsFromContext(ctx); claims != nil {
			adminID = claims.Subject
		}
		s.repo.auditor.Security(ctx, auditport.SecurityEntry{
			ActorType: "admin", ActorID: adminID,
			Action:   "delivery.view_content",
			Metadata: map[string]any{"order_no": req.GetOrderNo(), "count": len(rows)},
		})
	}
	reply := &adminv1.ListDeliveriesReply{}
	for _, d := range rows {
		orderNo := s.getOrderNo(ctx, d.OrderID)
		content := s.decryptDelivery(ctx, d)
		masked := "****"
		if len(content) > 4 {
			masked = "****" + content[len(content)-4:]
		}
		reply.Deliveries = append(reply.Deliveries, &adminv1.DeliveryRecord{
			Id: d.ID, OrderNo: orderNo, CardId: d.CardID,
			Content: content, ContentMasked: masked,
			DeliveredMode: string(d.DeliveredMode), DeliveredBy: d.DeliveredBy,
			FetchCount: d.FetchCount, FetchedIp: d.FetchedIP,
		})
		if !d.DeliveredAt.IsZero() {
			reply.Deliveries[len(reply.Deliveries)-1].DeliveredAt = d.DeliveredAt.Unix()
		}
	}
	return reply, nil
}

// decryptDelivery 交付记录明文（卡密现场解密；直发取商品 direct_content；
// 即删模式卡密已物理删除）。
func (s *AdminFulfillmentService) decryptDelivery(ctx context.Context, d *ent.OrderDelivery) string {
	client := data.Client(ctx, s.data)
	if d.DeliveredMode == orderdelivery.DeliveredModeDirect && d.CardID == 0 {
		var productID uint64
		if d.ItemID > 0 {
			if it, e := client.OrderItem.Get(ctx, d.ItemID); e == nil {
				productID = it.ProductID
			}
		}
		p, err := client.Product.Get(ctx, productID)
		if err != nil || len(p.DirectContent) == 0 {
			return "（直发内容未配置）"
		}
		plain, err := s.repo.cipher.Open(p.DirectContent, p.ID, p.SubsiteID)
		if err != nil {
			return "（解密失败）"
		}
		return string(plain)
	}
	c, err := client.Card.Get(ctx, d.CardID)
	if ent.IsNotFound(err) {
		return "（卡密已删除）"
	}
	if err != nil {
		return ""
	}
	plain, err := s.repo.cipher.Open(c.Content, c.ProductID, c.SubsiteID)
	if err != nil {
		return "（解密失败）"
	}
	return string(plain)
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
