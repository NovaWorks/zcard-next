//go:build integration

package supply

import (
	"context"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	kerrors "github.com/go-kratos/kratos/v3/errors"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestStoreChannelHTTPContractRetailPricing(t *testing.T) {
	if os.Getenv("ZCARD_HTTPX_ALLOW_PRIVATE") != "1" {
		t.Skip("isolated loopback fixture requires explicit mode")
	}
	t.Setenv("ZCARD_SMS_SALES_ENABLED", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Supply-Signature") != adapter.ZCardSign("secret", r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Supply-Timestamp"), r.Header.Get("X-Supply-Nonce"), body) {
			t.Error("bad signed request")
		}
		switch r.URL.Path {
		case "/api/supply/ping":
			fmt.Fprint(w, `{"ok":true,"balance":"10000","currency":"CNY","capabilities":["sms_channel_catalog.v1","sms_channel_purchase.v1","sms_polling.v1"]}`)
		case "/api/supply/products/17/sms/options":
			fmt.Fprint(w, `{"countries":[{"id":"1","name":"US"}],"platforms":[{"id":"2","name":"WA"}]}`)
		case "/api/supply/products/17/sms/offers":
			fmt.Fprint(w, `{"offers":[{"offer_id":"opaque","price_cents":"123","stock":7,"name":"US / WA"}]}`)
		case "/api/supply/products/17/sms/quotes":
			fmt.Fprintf(w, `{"quote_id":"supplier-fixed","amount_cents":"123","currency":"CNY","expires_at":"%d","offer_name":"US / WA"}`, time.Now().Add(5*time.Minute).Unix())
		default:
			t.Errorf("browse allocated a number or unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	repo, d := newTestRepo(t)
	admin := NewAdminSupplyService(repo, nil)
	conn, e := admin.CreateConnection(context.Background(), &adminv1.CreateConnectionRequest{Name: "fixture", Driver: "zcard", BaseUrl: srv.URL, Credentials: `{"api_key":"key","api_secret":"secret"}`, ExchangeRate: 1})
	if e != nil {
		t.Fatal(e)
	}
	p := d.Client.Product.Create().SetName("SMS channel").SetSlug("channel").SetIsLocked(true).SetProductKind("sms_channel").SetDeliveryKind("sms_activation").SetPrice(0).SetUpstreamSourceID(conn.Id).SetUpstreamProductCode("17").SetStatus(1).SaveX(context.Background())
	d.Client.SupplyMapping.Create().SetConnectionID(conn.Id).SetUpstreamProduct("17").SetLocalProductID(p.ID).SetPricingOverride(map[string]any{"rule": map[string]any{"mode": "fixed", "amount": 200}}).SaveX(context.Background())
	s := NewStoreSMSChannelService(NewGateway(repo, nil, catalog.NewProductRepoImpl(d, nil)))
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 1, Realm: authn.RealmUser})
	req := &storefrontv1.SMSChannelBrowseRequest{ProductId: p.ID, CountryId: "1", PlatformId: "2", Page: 1, PageSize: 50}
	if out, e := s.Options(context.Background(), req); e != nil || len(out.Countries) != 1 {
		t.Fatal("guest browse failed", e)
	}
	if out, e := s.Offers(context.Background(), req); e != nil || len(out.Offers) != 1 || out.Offers[0].PriceCents != 323 {
		t.Fatal("guest retail pricing failed", e)
	}
	if _, e := s.Quote(context.Background(), &storefrontv1.SMSChannelQuoteRequest{ProductId: p.ID, OfferId: "opaque"}); kerrors.Code(e) != 401 {
		t.Fatal("guest quote accepted", e)
	}
	public := khttp.NewServer()
	storefrontv1.RegisterStoreSMSChannelServiceHTTPServer(public, s)
	for _, path := range []string{"options", "offers"} {
		w := httptest.NewRecorder()
		public.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/api/v1/storefront/products/%d/sms/%s?country_id=1&platform_id=2", p.ID, path), nil))
		if w.Code != 200 {
			t.Fatalf("guest HTTP %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if d.Client.SMSRetailQuote.Query().CountX(context.Background()) != 0 {
		t.Fatal("guest created quotes")
	}
	for _, status := range []int8{0, 2, -1} {
		d.Client.Product.UpdateOneID(p.ID).SetStatus(status).ExecX(context.Background())
		if _, e := s.Options(context.Background(), req); kerrors.Code(e) != 404 {
			t.Fatalf("guest saw status %d: %v", status, e)
		}
	}
	d.Client.Product.UpdateOneID(p.ID).SetStatus(1).ExecX(context.Background())
	if out, e := s.Options(ctx, req); e != nil || len(out.Platforms) != 1 {
		t.Fatal(e)
	}
	if out, e := s.Offers(ctx, req); e != nil || out.Offers[0].PriceCents != 323 {
		t.Fatal("retail markup not applied", e)
	}
	out, e := s.Quote(ctx, &storefrontv1.SMSChannelQuoteRequest{ProductId: p.ID, CountryId: "1", PlatformId: "2", OfferId: "opaque", Page: 1, PageSize: 50})
	if e != nil {
		t.Fatal(e)
	}
	q := d.Client.SMSRetailQuote.GetX(ctx, out.QuoteId)
	if q.CostCents != 123 || q.AmountCents != 323 || q.UserID != 1 || q.UpstreamQuoteID != "supplier-fixed" {
		t.Fatal("quote did not retain account/cost/retail binding")
	}
}
