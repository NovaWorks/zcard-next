package fulfillment

import (
	"context"
	"encoding/json"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
	"strings"
	"time"
)

func (r *DeliveryRepoImpl) serviceTarget(ctx context.Context, no string, id, admin uint64) (*ent.Order, *ent.OrderItem, error) {
	c := data.Client(ctx, r.data)
	o, e := c.Order.Query().Where(order.OrderNo(no)).Only(ctx)
	if e != nil {
		return nil, nil, e
	}
	if e = r.lockDeliveryOrder(ctx, o); e != nil {
		return nil, nil, e
	}
	it, e := c.OrderItem.Query().Where(orderitem.ID(id), orderitem.OrderID(o.ID), orderitem.FulfillmentTypeEQ(orderitem.FulfillmentTypeManual)).Only(ctx)
	if e != nil {
		return nil, nil, fmt.Errorf("该商品项不是人工服务")
	}
	if it.FulfillmentStatus != "pending" && it.FulfillmentStatus != "delivering" {
		return nil, nil, fmt.Errorf("商品项已处理，请刷新")
	}
	if it.AssignedAdminID != 0 && it.AssignedAdminID != admin {
		return nil, nil, fmt.Errorf("该服务已由其他管理员领取")
	}
	pending, e := c.RefundOrder.Query().Where(refundorder.OrderID(o.ID), refundorder.StatusIn(refundorder.StatusCreated, refundorder.StatusProcessing)).Exist(ctx)
	if e != nil {
		return nil, nil, e
	}
	if pending {
		return nil, nil, fmt.Errorf("订单有退款待核对，不能处理")
	}
	return o, it, nil
}
func (s *AdminFulfillmentService) StartService(ctx context.Context, req *adminv1.StartServiceRequest) (*emptypb.Empty, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "请登录")
	}
	e := data.Tx(ctx, s.data, func(ctx context.Context) error {
		o, it, e := s.repo.serviceTarget(ctx, req.OrderNo, req.OrderItemId, claims.Subject)
		if e != nil {
			return e
		}
		if it.FulfillmentStatus == "delivering" {
			return nil
		}
		c := data.Client(ctx, s.data)
		if e = c.OrderItem.UpdateOneID(it.ID).SetAssignedAdminID(claims.Subject).SetFulfillmentStatus("delivering").Exec(ctx); e != nil {
			return e
		}
		return c.OrderStatusEvent.Create().SetOrderID(o.ID).SetFromStatus(string(o.Status)).SetToStatus(string(o.Status)).SetEvent("service_started").SetOperator("admin").SetOperatorID(claims.Subject).SetReason(fmt.Sprintf("开始处理商品项 %d", it.ID)).Exec(ctx)
	})
	if e != nil {
		return nil, errors.BadRequest("fulfillment.START_FAILED", e.Error())
	}
	return &emptypb.Empty{}, nil
}
func (r *DeliveryRepoImpl) CompleteService(ctx context.Context, no string, id uint64, content, remark string, admin uint64) error {
	content = strings.TrimSpace(content)
	if content == "" || len([]rune(content)) > 4000 || len([]rune(remark)) > 180 {
		return fmt.Errorf("请填写4000字以内的交付说明，内部备注不超过180字")
	}
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		o, it, e := r.serviceTarget(ctx, no, id, admin)
		if e != nil {
			return e
		}
		if it.FulfillmentStatus != "delivering" {
			return fmt.Errorf("请先开始处理该服务")
		}
		c := data.Client(ctx, r.data)
		if r.cipher == nil {
			return fmt.Errorf("交付加密不可用")
		}
		sealed, e := r.cipher.Seal(content, it.ProductID, o.SubsiteID)
		if e != nil {
			return e
		}
		if e = c.OrderDelivery.Create().SetOrderID(o.ID).SetItemID(it.ID).SetCardID(0).SetDeliveryTokenHash(hashToken(randomToken())).SetDeliveredMode("status").SetServiceContent(sealed).SetDeliveredQuantity(it.Quantity).SetDeliveredBy(admin).SetDeliveredAt(time.Now().UTC()).Exec(ctx); e != nil {
			return e
		}
		if e = c.OrderItem.UpdateOneID(it.ID).SetAssignedAdminID(admin).Exec(ctx); e != nil {
			return e
		}
		return r.updateDeliveryProgress(ctx, o, "admin", admin, remark)
	})
}
func (r *DeliveryRepoImpl) completionEvent(ctx context.Context, o *ent.Order) error {
	email := o.Contact
	if !strings.Contains(email, "@") {
		email = o.GuestContact
	}
	if o.UserID > 0 {
		if u, e := data.Client(ctx, r.data).User.Get(ctx, o.UserID); e == nil {
			email = u.Email
		}
	}
	payload, _ := json.Marshal(map[string]any{"order_no": o.OrderNo, "order_id": o.ID, "user_id": o.UserID, "subsite_id": o.SubsiteID, "email": email})
	return data.NewOutboxWriter(r.data).Write(ctx, "fulfillment", "order.delivered", o.OrderNo, "order:"+o.OrderNo+":delivered", payload)
}
func (r *DeliveryRepoImpl) serviceText(ctx context.Context, d *ent.OrderDelivery) string {
	c := data.Client(ctx, r.data)
	it, e := c.OrderItem.Get(ctx, d.ItemID)
	if e != nil {
		return "（交付信息暂不可用）"
	}
	plain, e := r.cipher.Open(d.ServiceContent, it.ProductID, it.SubsiteID)
	if e != nil {
		return "（解密失败）"
	}
	return plain
}
func deliveryKind(d *ent.OrderDelivery) string {
	if len(d.ServiceContent) > 0 {
		return "service"
	}
	if len(d.Logistics) > 0 {
		return "logistics"
	}
	if string(d.DeliveredMode) == "direct" {
		return "link"
	}
	return "card"
}
