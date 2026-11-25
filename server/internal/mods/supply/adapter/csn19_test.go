package adapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
)

const csnCatalogFixture = `[{"service":1,"name":"Followers","description":"<p>Provider service description</p>","type":"Default","category":"First Category","rate":"0.015","min":"50","max":"10000","refill":true,"cancel":true},{"service":"2","name":"Comments","type":"Custom Comments","category":"Second Category","rate":"8","min":"2","max":"1500","refill":false,"cancel":true},{"service":3,"name":"Package","type":"Package","category":"First Category","rate":"5","min":"1","max":"1","refill":false,"cancel":false},{"service":4,"name":"Subscription","type":"Subscriptions","category":"First Category","rate":"1","min":"1","max":"1000"}]`

func csnFixture(t *testing.T, add func(http.ResponseWriter, *http.Request)) (*csn19Adapter, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v2" {
			t.Errorf("invalid endpoint %s %s", r.Method, r.URL.Path)
		}
		_ = r.ParseForm()
		if r.PostForm.Get("key") != "fixture-secret" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Error("missing form credentials")
		}
		switch r.PostForm.Get("action") {
		case "balance":
			_, _ = w.Write([]byte(`{"balance":"100.84292","currency":"USD"}`))
		case "services":
			_, _ = w.Write([]byte(csnCatalogFixture))
		case "add":
			calls++
			add(w, r)
		case "status":
			_, _ = w.Write([]byte(`{"charge":"0.27819","start_count":"3572","status":"Partial","remains":"157","currency":"USD"}`))
		case "refill":
			_, _ = w.Write([]byte(`{"refill":"77"}`))
		case "refill_status":
			if r.PostForm.Get("refill") != "77" {
				t.Error("refill ID lost")
			}
			_, _ = w.Write([]byte(`{"status":"Completed"}`))
		case "cancel":
			if r.PostForm.Get("orders") != "23501" {
				t.Error("cancel order lost")
			}
			_, _ = w.Write([]byte(`[{"order":23501,"cancel":1}]`))
		default:
			t.Error("unexpected action", r.PostForm.Get("action"))
			w.WriteHeader(400)
		}
	}))
	t.Cleanup(srv.Close)
	return &csn19Adapter{key: "fixture-secret", t: newTransportWithClient(srv.URL+"/api/v2", []int{0, 0}, nil, srv.Client())}, &calls
}
func TestCSN19DocumentedContract(t *testing.T) {
	a, calls := csnFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.PostForm.Get("service") != "1" || r.PostForm.Get("quantity") != "1000" || r.PostForm.Get("link") != "https://example.com/a?x=1&y=2" {
			t.Error("purchase parameters changed")
		}
		if r.PostForm.Has("request_no") || r.PostForm.Has("trade_no") {
			t.Error("fabricated provider idempotency")
		}
		_, _ = w.Write([]byte(`{"order":23501}`))
	})
	ctx := context.Background()
	p, e := a.Ping(ctx)
	if e != nil || p.Balance != 10084 || p.Currency != "USD" {
		t.Fatal(p, e)
	}
	list, e := a.ListProducts(ctx, 1, 20, false)
	if e != nil || len(list.Items) != 4 || len(list.Categories) != 2 || list.Items[0].ProductKind != "social_service" || list.Items[0].Price != 0 {
		t.Fatal(list, e)
	}
	if list.Items[0].Description != "<p>Provider service description</p>" || !list.Items[0].DescriptionSet {
		t.Fatal("provider description dropped", list.Items[0])
	}
	services, e := a.SocialServices(ctx)
	if e != nil {
		t.Fatal(e)
	}
	s := services[0].Schema
	amount, e := supplyport.SocialExactAmount(s, supplyport.SocialParameters{"quantity": "1000", "link": "https://example.com"})
	if e != nil || amount != "0.015" {
		t.Fatal(amount, e)
	}
	if services[2].Schema.Purchasable || services[3].Schema.Purchasable {
		t.Fatal("unverified package/subscription billing opened")
	}
	result, e := a.CreateProviderSocial(ctx, supplyport.SocialProviderPurchase{ProductID: "1", SchemaRevision: s.Revision, Schema: s, Parameters: supplyport.SocialParameters{"quantity": "1000", "link": "https://example.com/a?x=1&y=2"}})
	if e != nil || result.UpstreamOrderID != "23501" || *calls != 1 {
		t.Fatal(result, e, *calls)
	}
	status, e := a.QueryProviderSocial(ctx, "23501")
	if e != nil || status.Charge != "0.27819" || status.Snapshot.State != "partial" || *status.Snapshot.Remains != 157 || status.Snapshot.RefundedAmountCents != 0 {
		t.Fatal(status, e)
	}
	refill, e := a.ActProviderSocial(ctx, "23501", "refill", "local-action")
	if e != nil || refill.OperationID != "77" {
		t.Fatal(refill, e)
	}
	refill, e = a.QueryProviderSocialOperation(ctx, "77", "refill")
	if e != nil || refill.Status != "succeeded" {
		t.Fatal(refill, e)
	}
	cancel, e := a.ActProviderSocial(ctx, "23501", "cancel", "local-cancel")
	if e != nil || cancel.Status != "pending" {
		t.Fatal(cancel, e)
	}
	if _, e = a.CreateOrder(ctx, CreateOrderReq{}); !errors.Is(e, ErrNotSupported) {
		t.Fatal("service purchased as a card", e)
	}
}
func TestCSN19UnknownSubmissionIsNeverRetried(t *testing.T) {
	for _, body := range []string{`{"error":"fixture-secret echoed","order":23501}`, `{}`, `not json`, `{"order":"0"}`, `server-error`} {
		t.Run(body, func(t *testing.T) {
			a, calls := csnFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				if body == "server-error" {
					w.WriteHeader(503)
				}
				_, _ = w.Write([]byte(body))
			})
			services, e := a.SocialServices(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			s := services[0].Schema
			_, e = a.CreateProviderSocial(context.Background(), supplyport.SocialProviderPurchase{ProductID: "1", SchemaRevision: s.Revision, Schema: s, Parameters: supplyport.SocialParameters{"link": "https://example.com", "quantity": "100"}})
			if !errors.Is(e, supplyport.ErrSocialSubmitUnknown) || strings.Contains(e.Error(), "fixture-secret") || *calls != 1 {
				t.Fatal(e, *calls)
			}
		})
	}
}
func TestCSN19CommentsAndRedirect(t *testing.T) {
	a, calls := csnFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.PostForm.Get("comments") != "same\r\nsame\n中文" || r.PostForm.Has("quantity") {
			t.Error("comments mutated or fabricated quantity")
		}
		_, _ = w.Write([]byte(`{"order":"123"}`))
	})
	rows, e := a.SocialServices(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	s := rows[1].Schema
	_, e = a.CreateProviderSocial(context.Background(), supplyport.SocialProviderPurchase{ProductID: "2", SchemaRevision: s.Revision, Schema: s, Parameters: supplyport.SocialParameters{"link": "https://example.com", "comments": "same\r\nsame\n中文"}})
	if e != nil || *calls != 1 {
		t.Fatal(e, *calls)
	}
	_, e = a.CreateProviderSocial(context.Background(), supplyport.SocialProviderPurchase{ProductID: "2", SchemaRevision: s.Revision, Schema: s, Parameters: supplyport.SocialParameters{"link": "https://example.com", "comments": "first\n\nsecond"}})
	if e == nil || *calls != 1 {
		t.Fatal("blank comment sent", e, *calls)
	}
	forwarded := false
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded = true }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer source.Close()
	a.t = newTransportWithClient(source.URL, nil, nil, source.Client())
	if _, e = a.Ping(context.Background()); e == nil || forwarded {
		t.Fatal("redirect forwarded body credentials", e)
	}
}
func TestCSN19AddressAndKey(t *testing.T) {
	for _, base := range []string{"https://8.8.8.8", "https://8.8.8.8/api", "https://8.8.8.8/api/v2/"} {
		a, e := newCSN19(base, Credentials{APIKey: "fixture"}, nil)
		if e != nil || a.(*csn19Adapter).t.baseURL != "https://8.8.8.8/api/v2" {
			t.Fatal(base, a, e)
		}
	}
	if _, e := newCSN19("https://8.8.8.8", Credentials{}, nil); !errors.Is(e, ErrCredentialsInvalid) {
		t.Fatal(e)
	}
}

func TestCSN19InvalidCommentsAreDefinitivelyUnsubmitted(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		a, calls := csnFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid comment submitted") })
		services, err := a.SocialServices(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		schema := services[1].Schema
		if !schema.Fields[1].DisallowBlankLines {
			t.Fatal("blank-line rule missing from public schema")
		}
		if legacy {
			schema.Fields[1].DisallowBlankLines = false
		}
		schema.Revision = supplyport.SocialSchemaRevision(schema)
		for _, comments := range []string{"first\n\nsecond", "first\nsecond\n", "first\n \nsecond"} {
			_, err = a.CreateProviderSocial(context.Background(), supplyport.SocialProviderPurchase{ProductID: "2", SchemaRevision: schema.Revision, Schema: schema, Parameters: supplyport.SocialParameters{"link": "https://example.com/video", "comments": comments}})
			var rejected *supplyport.SocialRejected
			if !errors.As(err, &rejected) || rejected.Code != "invalid_parameters" || *calls != 0 {
				t.Fatalf("local failure became unknown or was sent: legacy=%v err=%v calls=%d", legacy, err, *calls)
			}
		}
	}
}
