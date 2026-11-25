package order

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	entorder "github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

func socialReadOrder(t *testing.T) (*data.Data, *OrderUsecase, string, string) {
	t.Helper()
	d, uc, pid := newGateEnv(t)
	ctx := context.Background()
	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	uc.AccessCipher = cipher
	p := d.Client.Product.UpdateOneID(pid).SetDeliveryKind("social_service").SetProductKind("social_service").SetPrice(0).SaveX(ctx)
	conn := d.Client.SupplyConnection.Create().SetID(9).SetName("wholesale account").SetDriver("zcard").SetStatus("active").SetBaseURL("https://example.test").SetCredentials([]byte("private-credentials")).SetExchangeRate(1).SaveX(ctx)
	mapping := d.Client.SupplyMapping.Create().SetConnectionID(9).SetUpstreamProduct(p.UpstreamProductCode).SetUpstreamSku("").SetLocalProductID(pid).SetPricingOverride(map[string]any{"rule": map[string]any{"mode": "equal"}}).SaveX(ctx)
	schema := supplyport.SocialSchema{Version: 1, Revision: "frozen-schema", Profile: "Default", Purchasable: true, Fields: []supplyport.SocialField{{Key: "link", Label: "目标链接", Type: "url", Required: true}, {Key: "quantity", Label: "服务数量", Type: "integer", Required: true}, {Key: "comments", Label: "评论内容", Type: "lines"}}, Quantity: supplyport.SocialQuantity{Mode: "explicit", Field: "quantity"}}
	link := "https://www.tiktok.com/@buyer/video/123456789?lang=zh&source=order"
	params := supplyport.SocialParameters{"link": link, "quantity": "50", "comments": "same\r\nsame\n中文 & = +"}
	raw, _ := json.Marshal(params)
	sealed, err := cipher.Seal(string(raw), pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(schema)
	d.Client.SocialQuote.Create().SetID("read-quote-B").SetRole("retail").SetUserID(1).SetProductID(pid).SetConnectionID(conn.ID).SetConnectionIdentity(data.SocialConnectionIdentity(conn)).SetProductRevision(p.LockVersion).SetSchemaRevision(schema.Revision).SetPricingRevision(data.SocialRetailPricingRevision(conn, mapping)).SetUpstreamProductID(p.UpstreamProductCode).SetParamsCipher(sealed).SetSchemaJSON(string(raw)).SetSupplyQuoteID("private-quote-A").SetCurrency("CNY").SetAmountCents(999).SetCostCents(123).SetExpiresAt(time.Now().Add(time.Minute).Unix()).SaveX(ctx)
	result, err := uc.CreateOrder(ctx, CreateOrderInput{SocialQuoteID: "read-quote-B", UserID: 1, IdempotencyKey: "read-frozen-params", Items: []OrderItemInput{{ProductID: pid, Quantity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	return d, uc, result.OrderNo, link
}

func TestSocialOrderHistoryShowsOriginalPurchaseFields(t *testing.T) {
	d, uc, no, link := socialReadOrder(t)
	ctx := context.Background()
	owner := identity.WithClaims(ctx, &authn.Claims{Subject: 1, Realm: authn.RealmUser})
	svc := NewStoreOrderService(uc, nil)
	item := d.Client.OrderItem.Query().OnlyX(ctx)
	// The read must survive catalog and connection edits, without copying plaintext.
	d.Client.Product.UpdateOneID(item.ProductID).SetName("changed catalog").SetSocialSchema(map[string]any{}).SetStatus(0).ExecX(ctx)
	d.Client.SupplyConnection.UpdateOneID(9).SetStatus("disabled").ExecX(ctx)
	for _, status := range []entorder.Status{entorder.StatusPendingPayment, entorder.StatusDelivered, entorder.StatusRefunded} {
		d.Client.Order.UpdateOneID(item.OrderID).SetStatus(status).ExecX(ctx)
		detail, err := svc.GetOrder(owner, &storefrontv1.GetOrderRequest{OrderNo: no})
		if err != nil {
			t.Fatal(status, err)
		}
		var fields []map[string]string
		if len(detail.Items) != 1 || json.Unmarshal([]byte(detail.Items[0].FormAnswersJson), &fields) != nil || len(fields) != 3 {
			t.Fatalf("missing original fields: %v", detail)
		}
		if fields[0]["value"] != link || fields[0]["type"] != "url" || fields[1]["value"] != "50" || fields[2]["value"] != "same\r\nsame\n中文 & = +" {
			t.Fatal("original parameters changed", fields)
		}
		for _, secret := range []string{"private-quote-A", "private-credentials", "max_supply_amount", "connection_id"} {
			if strings.Contains(detail.Items[0].FormAnswersJson, secret) {
				t.Fatal("private purchase data exposed", secret)
			}
		}
		history, err := svc.ListMyOrders(owner, &storefrontv1.ListMyOrdersRequest{})
		if err != nil || len(history.GetOrders()) != 1 || history.Orders[0].TargetLink != link {
			t.Fatalf("history target missing: %v %v", history, err)
		}
	}
	saved := d.Client.OrderItem.GetX(ctx, item.ID)
	if len(saved.FormAnswers) != 0 || strings.Contains(saved.SocialPurchaseSnapshot, link) {
		t.Fatal("read copied private parameters into plaintext storage")
	}
	for _, other := range []context.Context{identity.WithClaims(ctx, &authn.Claims{Subject: 2, Realm: authn.RealmUser}), tenancy.WithContext(owner, tenancy.Context{SubsiteID: 99})} {
		detail, err := svc.GetOrder(other, &storefrontv1.GetOrderRequest{OrderNo: no})
		if err == nil {
			raw, _ := json.Marshal(detail)
			if strings.Contains(string(raw), link) {
				t.Fatal("foreign order target leaked")
			}
		}
		history, err := svc.ListMyOrders(other, &storefrontv1.ListMyOrdersRequest{})
		if err == nil && len(history.GetOrders()) != 0 {
			t.Fatal("foreign history exposed")
		}
	}
}
