package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
			var req channelQuoteContractRequest
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("quote request violates supply contract: %v", err)
				http.Error(w, "invalid quote request", http.StatusBadRequest)
				return
			}
			if req.OfferID != "opaque" || req.CountryID != 1 || req.PlatformID != 2 || req.Page != 2001 || req.PageSize != 50 {
				t.Errorf("quote filters changed: %+v", req)
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

// The supply API requires JSON integers even though storefront filter IDs are strings.
type channelQuoteContractRequest struct {
	OfferID    string `json:"offer_id"`
	CountryID  int64  `json:"country_id"`
	PlatformID int64  `json:"platform_id"`
	Page       int32  `json:"page"`
	PageSize   int32  `json:"page_size"`
}

func TestZCardChannelQuoteNumericFilters(t *testing.T) {
	for _, tc := range []struct {
		name, country, platform   string
		wantCountry, wantPlatform int64
		invalidField              string
	}{
		{name: "selected", country: "1", platform: "2", wantCountry: 1, wantPlatform: 2},
		{name: "empty filters"},
		{name: "empty country", platform: "2", wantPlatform: 2},
		{name: "empty platform", country: "1", wantCountry: 1},
		{name: "zero filters", country: "0", platform: "0"},
		{name: "large integers", country: "9007199254740993", platform: "9223372036854775807", wantCountry: 9007199254740993, wantPlatform: 9223372036854775807},
		{name: "invalid country", country: "US", platform: "2", invalidField: "country_id"},
		{name: "fractional country", country: "1.5", platform: "2", invalidField: "country_id"},
		{name: "negative country", country: "-1", platform: "2", invalidField: "country_id"},
		{name: "overflow country", country: "9223372036854775808", platform: "2", invalidField: "country_id"},
		{name: "invalid platform", country: "1", platform: "WA", invalidField: "platform_id"},
		{name: "fractional platform", country: "1", platform: "2.5", invalidField: "platform_id"},
		{name: "negative platform", country: "1", platform: "-2", invalidField: "platform_id"},
		{name: "overflow platform", country: "1", platform: "9223372036854775808", invalidField: "platform_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/supply/products/17/sms/quotes" {
					t.Errorf("unexpected quote request: %s %s", r.Method, r.URL.Path)
				}
				var req channelQuoteContractRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("quote request violates supply contract: %v", err)
					http.Error(w, "invalid quote request", http.StatusBadRequest)
					return
				}
				if req.OfferID != "opaque" || req.CountryID != tc.wantCountry || req.PlatformID != tc.wantPlatform || req.Page != 1 || req.PageSize != 50 {
					t.Errorf("quote filters changed: %+v", req)
				}
				fmt.Fprintf(w, `{"quote_id":"fixed-quote","amount_cents":123,"currency":"CNY","expires_at":%d}`, time.Now().Add(time.Minute).Unix())
			}))
			defer srv.Close()
			a := &zCardAdapter{creds: Credentials{APIKey: "key", APISecret: "secret"}, t: newTransportWithClient(srv.URL, []int{0}, nil, srv.Client())}
			q, err := a.SMSChannelQuote(context.Background(), "17", "opaque", supplyport.SMSChannelFilter{CountryID: tc.country, PlatformID: tc.platform, Page: 1, PageSize: 50})
			if tc.invalidField != "" {
				if err == nil || !strings.Contains(err.Error(), tc.invalidField) || q != nil {
					t.Fatalf("invalid %s accepted: quote=%+v err=%v", tc.invalidField, q, err)
				}
				if calls.Load() != 0 {
					t.Fatal("invalid filter sent an upstream request")
				}
				return
			}
			if err != nil || q == nil || q.QuoteID != "fixed-quote" || calls.Load() != 1 {
				t.Fatalf("valid filters rejected: quote=%+v err=%v calls=%d", q, err, calls.Load())
			}
		})
	}
}
