package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAcgAuthenticatedQuotePerSKU(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("app_id") != "account" || r.Form.Get("sign") == "" {
			t.Error("missing account authentication")
		}
		switch r.URL.Path {
		case "/shared/commodity/inventory":
			if r.Form.Get("sharedCode") != "P" {
				t.Error("wrong product code")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"config": "[category]\nA=10\nB=20\n[sku]\nRegion.CN=2\n[category_factory]\nA=3\nB=4", "price": 10, "factory_price": 1}})
		case "/shared/commodity/valuation":
			if r.Form.Get("num") != "1" || r.Form.Get("code") != "P" || r.Form.Get("sku[Region]") != "CN" {
				t.Errorf("wrong quote parameters: %v", r.Form)
			}
			calls = append(calls, r.Form.Get("race"))
			price := "7.25"
			if r.Form.Get("race") == "B" {
				price = "15.50"
			}
			fmt.Fprintf(w, `{"code":200,"data":{"price":%q}}`, price)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	a := &acgFakaAdapter{creds: Credentials{AppID: "account", AppKey: "test-secret"}, t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	p := &Product{ID: "P", Price: 1000, FactoryPrice: 100, IsActive: true}
	got, err := a.QuoteProduct(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || len(got.SKUs) != 2 || got.Price != 725 || got.FactoryPrice != 725 || got.SKUs[0].Price != 725 || got.SKUs[1].Price != 1550 {
		t.Fatalf("account/SKU quotes not used: %+v", got)
	}
	if p.Price != 1000 || len(p.SKUs) != 0 {
		t.Fatal("quote mutated cached catalog")
	}
}

func TestAcgQuoteFailureNeverFallsBack(t *testing.T) {
	for _, raw := range []string{`null`, `0`, `-1`, `"NaN"`, `"1.x"`, `"999999999999999999999"`, `"1.234"`} {
		t.Run(raw, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/shared/commodity/inventory" {
					fmt.Fprint(w, `{"code":200,"data":{"config":"","factory_price":5}}`)
					return
				}
				fmt.Fprintf(w, `{"code":200,"data":{"price":%s}}`, raw)
			}))
			defer srv.Close()
			a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			if p, err := a.QuoteProduct(context.Background(), &Product{ID: "P", Price: 1000, FactoryPrice: 500}); err == nil || p != nil {
				t.Fatalf("invalid quote fell back: %+v %v", p, err)
			}
		})
	}
}

func TestAcgPartialSKUQuoteReturnsNoProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.URL.Path == "/shared/commodity/inventory" {
			fmt.Fprint(w, `{"code":200,"data":{"config":"[category]\nA=10\nB=20"}}`)
			return
		}
		if r.Form.Get("race") == "B" {
			w.WriteHeader(403)
			return
		}
		fmt.Fprint(w, `{"code":200,"data":{"price":"7.00"}}`)
	}))
	defer srv.Close()
	a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	if p, err := a.QuoteProduct(context.Background(), &Product{ID: "P", Price: 1000}); err == nil || p != nil {
		t.Fatal("partial quotes must not form a product/SKU deletion snapshot")
	}
}
