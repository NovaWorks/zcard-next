package payment

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	ordermod "github.com/NovaWorks/zcard-next/server/internal/mods/order"
	settingsport "github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

func TestHTTPCallbackPaysAndDelivers(t *testing.T) {
	for _, method := range []string{"POST", "GET", "JSON"} {
		t.Run(method, func(t *testing.T) { testHTTPCallbackPaysAndDelivers(t, method) })
	}
}

func testHTTPCallbackPaysAndDelivers(t *testing.T, method string) {
	ctx := context.Background()
	d, repo, _, writer, _, _ := newCallbackEnv(t)
	repo.lifecycle = ordermod.ProvideOrderLifecycle(&ordermod.OrderUsecase{Data: d, Outbox: writer})
	driver, cfg, ack := "epay", `{"pid":"1","key":"test-secret"}`, "success"
	if method == "JSON" {
		driver, cfg, ack = "epusdt", `{"pid":"1","secret_key":"test-secret","currency":"cny"}`, "ok"
	}
	ch, err := repo.CreateChannel(ctx, "form gateway", "form-test", driver, cfg, 0, "fixed", true, 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	o, p := seedPendingOrder(t, d, "form-test", 1000)
	prod, err := d.Client.Product.Create().SetName("callback product").SetSlug("callback-product").SetPrice(1000).SetStatus(1).SetStockType("card").SetDeliveryMode("status").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal("test-card", prod.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Client.Card.Create().SetProductID(prod.ID).SetOrderID(o.ID).SetStatus("reserved").SetContent(sealed).SetContentHash(cipher.ContentHash("test-card")).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(prod.ID).SetQuantity(1).SetUnitPrice(1000).SetAmount(1000).SetFulfillmentType("auto").SetFulfillmentStatus("pending").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"pid": {"1"}, "out_trade_no": {o.OrderNo}, "trade_no": {"T-form"}, "money": {"10.00"}, "trade_status": {"TRADE_SUCCESS"}, "type": {"alipay"}}
	if method == "JSON" {
		form = url.Values{"order_id": {o.OrderNo}, "trade_id": {"T-json"}, "amount": {"10.00"}, "status": {"2"}}
	}
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+form.Get(k))
	}
	form.Set("sign", fmt.Sprintf("%x", md5.Sum([]byte(strings.Join(parts, "&")+"test-secret"))))
	form.Set("sign_type", "MD5")
	if method == "JSON" {
		mac := hmac.New(sha256.New, []byte("test-secret"))
		mac.Write([]byte(strings.Join(parts, "&")))
		form.Set("signature", fmt.Sprintf("%x", mac.Sum(nil)))
		form.Del("sign")
		form.Del("sign_type")
	}
	srv := khttp.NewServer()
	RegisterPaymentCallback(srv, repo, d)
	for i := 0; i < 2; i++ {
		requestMethod, target, contentType, body := "POST", fmt.Sprintf("/payments/callback/form-test?channel_id=%d", ch.ID), "application/x-www-form-urlencoded", form.Encode()
		if method == "GET" {
			requestMethod, target, body = "GET", target+"&"+body, ""
		}
		if method == "JSON" {
			contentType = "application/json"
			// Numeric 10.00 must retain its exact spelling during JSON signature verification.
			body = fmt.Sprintf(`{"order_id":%q,"trade_id":"T-json","amount":10.00,"status":2,"signature":%q}`, o.OrderNo, form.Get("signature"))
		}
		req := httptest.NewRequest(requestMethod, target, strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != 200 || rec.Body.String() != ack {
			t.Fatalf("callback HTTP %d: %s", rec.Code, rec.Body.String())
		}
	}
	got, err := repo.GetPayment(ctx, p.ID)
	if err != nil || got.Status != "success" {
		t.Fatalf("payment not successful: %v %+v", err, got)
	}
	paid, err := d.Client.Order.Get(ctx, o.ID)
	if err != nil || paid.Status != "paid" {
		t.Fatalf("order not paid: %v %+v", err, paid)
	}
	if len(writer.evts) != 1 || writer.evts[0].typ != events.OrderPaid {
		t.Fatalf("paid event count=%d", len(writer.evts))
	}
	deliver := fulfillment.NewDeliveryRepoImpl(d, cipher, nil, nil)
	for i := 0; i < 2; i++ {
		if err := deliver.OnOrderPaid(ctx, events.Envelope{Type: events.OrderPaid, Payload: writer.evts[0].payload}); err != nil {
			t.Fatal(err)
		}
	}
	delivered, err := d.Client.Order.Get(ctx, o.ID)
	if err != nil || delivered.Status != "delivered" {
		t.Fatalf("order not delivered: %v %+v", err, delivered)
	}
	count, err := d.Client.OrderDelivery.Query().Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("deliveries=%d err=%v", count, err)
	}
}

type callbackURLSettings struct {
	settingsport.Provider
	url string
}

func (s callbackURLSettings) Get(context.Context, string, string) (json.RawMessage, error) {
	return json.Marshal(s.url)
}

func TestCallbackURLSiteAddress(t *testing.T) {
	for _, tt := range []struct{ site, want string }{
		{"", "/payments/callback/epay"},
		{"kmigo.xyz", "https://kmigo.xyz/payments/callback/epay"},
		{"  kmigo.xyz/  ", "https://kmigo.xyz/payments/callback/epay"},
		{"//kmigo.xyz/", "https://kmigo.xyz/payments/callback/epay"},
		{"https://kmigo.xyz/", "https://kmigo.xyz/payments/callback/epay"},
		{"http://localhost:8000/", "http://localhost:8000/payments/callback/epay"},
	} {
		t.Run(tt.site, func(t *testing.T) {
			repo := &PaymentRepoImpl{settings: callbackURLSettings{url: tt.site}}
			got := absolutePayURL(context.Background(), repo.CallbackURL(context.Background(), "epay"))
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseCallbackFormConsumedBody(t *testing.T) {
	req := httptest.NewRequest("POST", "/callback?source=gateway", strings.NewReader("money=10.00&sign=abc"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, _ := io.ReadAll(req.Body)
	form, err := parseCallbackForm(req, body)
	if err != nil || form["money"] != "10.00" || form["sign"] != "abc" || form["source"] != "gateway" {
		t.Fatalf("%v: %v", form, err)
	}
}
