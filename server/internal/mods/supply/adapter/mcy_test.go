package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/platform/mcysign"
)

const mcyFixtureItem = `{"id":12,"name":"月卡","introduce":"说明","picture_url":"/cover.png","category":{"id":7,"name":"数码"},"widget":"[{\"name\":\"account\",\"title\":\"账号\",\"type\":\"text\"}]","sku":[{"id":51,"name":"月卡","stock_price":"10.50","stock":"3"},{"id":52,"name":"年卡","stock_price":100,"stock":"大量"}]}`

func mcyFixture(t *testing.T, trade func(http.ResponseWriter, *http.Request)) (*mcyAdapter, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			t.Error("MCY requires POST form")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		params := map[string]string{}
		for k := range r.PostForm {
			params[k] = r.PostForm.Get(k)
		}
		if r.Header.Get("Api-Id") != "1001" || r.Header.Get("Api-Signature") != mcysign.Sign(params, "fixture-secret") {
			t.Error("MCY header signature mismatch")
		}
		if r.PostForm.Has("app_key") || r.PostForm.Has("app_id") || r.PostForm.Has("sign") {
			t.Error("MCY credentials must not be in form")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/plugin/open-api/connect":
			_, _ = w.Write([]byte(`{"code":200,"data":{"username":"MCY供货","balance":"123.45"}}`))
		case "/plugin/open-api/items":
			_, _ = w.Write([]byte(`{"code":200,"data":[` + mcyFixtureItem + `]}`))
		case "/plugin/open-api/item":
			if r.PostForm.Get("id") != "12" {
				t.Error("product id not preserved")
			}
			_, _ = w.Write([]byte(`{"code":200,"data":` + mcyFixtureItem + `}`))
		case "/plugin/open-api/sku/stock":
			if r.PostForm.Get("sku_id") != "52" {
				t.Error("stock queried wrong SKU")
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{"stock":"9"}}`))
		case "/plugin/open-api/sku/state":
			_, _ = w.Write([]byte(`{"code":200,"data":{"state":true}}`))
		case "/plugin/open-api/amount":
			amount := "9.25"
			if r.PostForm.Get("sku_id") == "52" {
				amount = "88.00"
			}
			if r.PostForm.Get("quantity") == "2" {
				amount = "176.00"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"amount": amount}})
		case "/plugin/open-api/trade":
			calls.Add(1)
			trade(w, r)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return &mcyAdapter{creds: Credentials{AppID: "1001", AppKey: "fixture-secret"}, t: newTransportWithClient(srv.URL, []int{0}, nil, srv.Client())}, calls
}

func TestMCYHeaderSignaturePHPVector(t *testing.T) {
	// Computed with mcy-shop Kernel\Util\Str::generateSignature in PHP.
	params := map[string]string{"sku_id": "52", "quantity": "2", "trade_no": "abc123", "account": "a+b &中文", "empty": "", "zero": "0"}
	if got := mcysign.Sign(params, "fixture-secret"); got != "9e82f05ba2e8354212bbc64e216121bc" {
		t.Fatal("PHP signature mismatch", got)
	}
}

func TestMCYCatalogSKUQuoteAndPurchase(t *testing.T) {
	a, calls := mcyFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.PostForm.Get("sku_id") != "52" || r.PostForm.Get("quantity") != "2" || r.PostForm.Get("trade_no") != md5Hex([]byte("PO-001"))[:24] || r.PostForm.Get("account") != "a+b &中文" {
			t.Error("purchase mapping changed")
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"contents":"CARD-A\r\nCARD-B"}}`))
	})
	ctx := context.Background()
	ping, err := a.Ping(ctx)
	if err != nil || ping.SiteName != "MCY供货" || ping.Balance != 12345 {
		t.Fatalf("ping: %+v %v", ping, err)
	}
	list, err := a.ListProducts(ctx, 1, 50, true)
	if err != nil || list.Total != 1 || list.HasMore || list.IncludesInactive || len(list.Items[0].SKUs) != 2 || list.Items[0].SKUs[1].Stock != -2 || list.Categories[0].Name != "数码" || list.Items[0].FormFields[0].Key != "account" {
		t.Fatalf("catalog: %+v %v", list, err)
	}
	p, err := a.QuoteProduct(ctx, &list.Items[0])
	if err != nil || p.Price != 925 || p.FactoryPrice != 925 || p.SKUs[1].Price != 8800 || list.Items[0].SKUs[1].Price != 10000 {
		t.Fatalf("account quote: %+v %v", p, err)
	}
	stock, err := a.GetStock(ctx, "12", "52")
	if err != nil || stock != 9 {
		t.Fatalf("stock: %d %v", stock, err)
	}
	result, err := a.CreateOrder(ctx, CreateOrderReq{ProductCode: "12", UpstreamSKU: "52", Quantity: 2, DownstreamOrderNo: "PO-001", ManualFormData: map[string]any{"account": "a+b &中文"}})
	if err != nil || result.Status != "delivered" || result.Amount != 17600 || len(result.Cards) != 2 || calls.Load() != 1 {
		t.Fatalf("purchase: %+v %v calls=%d", result, err, calls.Load())
	}
	if _, err := a.GetOrder(ctx, "original"); !errors.Is(err, ErrNotSupported) {
		t.Fatal("invented query endpoint")
	}
	if err := a.RefundOrder(ctx, "original"); !errors.Is(err, ErrNotSupported) {
		t.Fatal("invented refund endpoint")
	}
}

func TestMCYUncertainTradeNeverRetries(t *testing.T) {
	for _, mode := range []string{"502", "429", "html", "disconnect", "empty", "placeholder", "quantity", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			a, calls := mcyFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "502":
					w.WriteHeader(502)
				case "429":
					w.WriteHeader(429)
				case "html":
					_, _ = w.Write([]byte("<html>WAF</html>"))
				case "disconnect":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Fatal(err)
					}
					_ = conn.Close()
				case "empty":
					_, _ = w.Write([]byte(`{"code":200,"data":{}}`))
				case "placeholder":
					_, _ = w.Write([]byte(`{"code":200,"data":{"contents":"库存不足，请申请售后"}}`))
				case "quantity":
					_, _ = w.Write([]byte(`{"code":200,"data":{"contents":"ONE-CARD"}}`))
				case "rejected":
					_, _ = w.Write([]byte(`{"code":0,"msg":"订单已存在"}`))
				}
			})
			_, err := a.CreateOrder(context.Background(), CreateOrderReq{ProductCode: "12", UpstreamSKU: "52", Quantity: 2, DownstreamOrderNo: "PO-uncertain"})
			if !errors.Is(err, ErrPurchaseUncertain) || calls.Load() != 1 {
				t.Fatalf("uncertain purchase was retried/accepted: %v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestMCYRejectsAmbiguousSKUAndReservedForm(t *testing.T) {
	a, calls := mcyFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("must not trade") })
	for _, req := range []CreateOrderReq{
		{ProductCode: "12", Quantity: 1, DownstreamOrderNo: "P"},
		{ProductCode: "12", UpstreamSKU: "999", Quantity: 1, DownstreamOrderNo: "P"},
		{ProductCode: "12", UpstreamSKU: "52", Quantity: 1, DownstreamOrderNo: "P", ManualFormData: map[string]any{"trade_no": "hijack"}},
		{ProductCode: "12", UpstreamSKU: "52", Quantity: 1, DownstreamOrderNo: "P", ManualFormData: map[string]any{"account": []string{"unsigned"}}},
	} {
		if _, err := a.CreateOrder(context.Background(), req); err == nil {
			t.Fatal("accepted invalid purchase")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("submitted rejected purchase")
	}
}
