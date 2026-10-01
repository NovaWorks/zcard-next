package procurement

import (
	"context"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
)

// History survives product/source unlisting; access remains bound to the member.
func (s *StoreSMSService) ListSMSProduct(ctx context.Context, r *storefrontv1.SMSProductHistoryRequest) (*storefrontv1.SMSProductHistoryReply, error) {
	if tr, ok := transport.FromServerContext(ctx); ok {
		tr.ReplyHeader().Set("Cache-Control", "no-store, private")
	}
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil || claims.Subject == 0 || claims.Realm != authn.RealmUser {
		return nil, errors.Unauthorized("sms.LOGIN_REQUIRED", "请登录查看我的号码")
	}
	if r.ProductId == 0 || r.Page < 0 || r.Page > 1000000 {
		return nil, errors.BadRequest("sms.INVALID_PAGE", "商品或页码无效")
	}
	page := max(int(r.Page), 1)
	c := data.Client(ctx, s.svc.repo.data)
	items, e := c.OrderItem.Query().Where(orderitem.ProductID(r.ProductId), orderitem.SubsiteID(tenancy.FromContext(ctx).SubsiteID), orderitem.DeliveryKind("sms_activation"), orderitem.HasOrderWith(order.UserID(claims.Subject), order.StatusNEQ(order.StatusPendingPayment), order.StatusNEQ(order.StatusCanceled), order.StatusNEQ(order.StatusExpired))).Order(ent.Desc(orderitem.FieldID)).Offset((page - 1) * 20).Limit(20).All(ctx)
	if e != nil {
		return nil, e
	}
	out := &storefrontv1.SMSProductHistoryReply{}
	for _, it := range items {
		o, e := c.Order.Get(ctx, it.OrderID)
		if e != nil {
			return nil, e
		}
		snap, e := s.GetSMS(ctx, &storefrontv1.SMSOrderRequest{OrderNo: o.OrderNo})
		if e != nil {
			return nil, e
		}
		out.Orders = append(out.Orders, &storefrontv1.SMSRetailSession{OrderNo: o.OrderNo, OfferName: it.SmsProduct["offer_name"], AmountCents: o.TotalAmount, Sms: snap})
	}
	return out, nil
}
