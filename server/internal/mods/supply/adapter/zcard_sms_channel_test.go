package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestZCardChannelContractUsesSingleProductAndQuote(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Supply-Signature") != ZCardSign("secret", r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Supply-Timestamp"), r.Header.Get("X-Supply-Nonce"), body) {
			t.Error("bad HMAC")
		}
		switch r.URL.Path {
		case "/api/supply/products/17/sms/options":
			fmt.Fprint(w, `{"countries":[{"id":"1","name":"US"}],"platforms":[{"id":"2","name":"WA"}]}`)
		case "/api/supply/products/17/sms/offers":
			if r.URL.Query().Get("country_id") != "1" || r.URL.Query().Get("page") != "2001" {
				t.Error("filters missing")
			}
			fmt.Fprint(w, `{"offers":[{"offer_id":"opaque","price_cents":"123","stock":7,"name":"US / WA"}],"has_more":true}`)
		case "/api/supply/products/17/sms/quotes":
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			if req["offer_id"] != "opaque" {
				t.Error("missing offer")
			}
			fmt.Fprintf(w, `{"quote_id":"fixed-quote","amount_cents":"123","currency":"CNY","expires_at":"%d","offer_name":"US / WA"}`, time.Now().Add(time.Minute).Unix())
		case "/api/supply/sms/channel-orders":
			calls++
			var req supplyport.SMSPurchase
			_ = json.Unmarshal(body, &req)
			if req.SMSQuoteID != "fixed-quote" || req.RequiredCapability != supplyport.SMSProductPurchase || req.MaxSupplyAmountCents != 123 || req.DownstreamOrderNo != "fixed-intent" {
				t.Error("supplier intent changed")
			}
			fmt.Fprint(w, `{"supply_order_id":"42","amount":"123","charged":true,"status":"fulfilling"}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	a := &zCardAdapter{creds: Credentials{APIKey: "key", APISecret: "secret"}, t: newTransportWithClient(srv.URL, []int{0}, nil, srv.Client())}
	ctx := context.Background()
	f := supplyport.SMSChannelFilter{CountryID: "1", PlatformID: "2", Page: 2001, PageSize: 50}
	if out, e := a.SMSChannelOptions(ctx, "17", f); e != nil || len(out.Platforms) != 1 {
		t.Fatal("options", e)
	}
	if out, e := a.SMSChannelOffers(ctx, "17", f); e != nil || out.Offers[0].PriceCents != 123 || !out.HasMore {
		t.Fatal("offers", e)
	}
	q, e := a.SMSChannelQuote(ctx, "17", "opaque", f)
	if e != nil {
		t.Fatal(e)
	}
	if calls != 0 {
		t.Fatal("browsing allocated number")
	}
	req := supplyport.SMSPurchase{SMSQuoteID: q.QuoteID, ProductID: "17", Quantity: 1, DownstreamOrderNo: "fixed-intent", RequiredCapability: supplyport.SMSProductPurchase, MaxSupplyAmountCents: 123, Currency: "CNY"}
	for i := 0; i < 2; i++ {
		if _, e = a.CreateSMS(ctx, req); e != nil {
			t.Fatal(e)
		}
	}
}
