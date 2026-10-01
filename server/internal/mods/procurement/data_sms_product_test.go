package procurement

import (
	"context"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/go-kratos/kratos/v3/middleware"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"net/http/httptest"
	"testing"
)

func TestSMSProductHistoryOmitsExpiredUnpaidOrders(t *testing.T) {
	s, d, row := smsEnv(t)
	base := context.Background()
	d.Client.Order.UpdateOneID(row.OrderID).SetUserID(1).ExecX(base)
	productID := d.Client.OrderItem.GetX(base, row.OrderItemID).ProductID
	o := d.Client.Order.Create().SetOrderNo("expired-unpaid-sms").SetUserID(1).SetStatus("expired").SetTotalAmount(400).SaveX(base)
	d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(productID).SetQuantity(1).SetUnitPrice(400).SetAmount(400).SetFulfillmentType("upstream").SetDeliveryKind("sms_activation").SaveX(base)
	ctx := identity.WithClaims(base, &authn.Claims{Subject: 1, Realm: authn.RealmUser})
	out, e := NewStoreSMSService(s).ListSMSProduct(ctx, &storefrontv1.SMSProductHistoryRequest{ProductId: productID})
	if e != nil || len(out.Orders) != 1 || out.Orders[0].OrderNo == o.OrderNo {
		t.Fatal("unpaid expiry hid paid phone history", e, out)
	}
}

func TestEmptySMSProductHistoryDisablesCaching(t *testing.T) {
	s, _, _ := smsEnv(t)
	srv := khttp.NewServer(khttp.Middleware(func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			return next(identity.WithClaims(ctx, &authn.Claims{Subject: 1, Realm: authn.RealmUser}), req)
		}
	}))
	storefrontv1.RegisterStoreSMSServiceHTTPServer(srv, NewStoreSMSService(s))
	reply := httptest.NewRecorder()
	srv.ServeHTTP(reply, httptest.NewRequest("GET", "/api/v1/storefront/products/999999/sms/sessions", nil))
	if reply.Code != 200 || reply.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatal("empty phone history can be cached", reply.Code, reply.Header(), reply.Body.String())
	}
}
