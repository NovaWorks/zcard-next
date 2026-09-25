package adapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Real old-skin shape: HTTP 200 with a base64 HTML 404, never execute the JS.
func legacyMissingPage(w http.ResponseWriter) {
	fmt.Fprintf(w, `<script>var page='%s'</script>`, base64.StdEncoding.EncodeToString([]byte(`<html><title>404 Not Found</title></html>`)))
}

func TestLegacyAcgQuotesAndInventory(t *testing.T) {
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		calls[r.URL.Path]++
		switch r.URL.Path {
		case "/shared/commodity/valuation", "/shared/commodity/stock":
			legacyMissingPage(w)
		case "/shared/commodity/item":
			fmt.Fprint(w, `{"code":200,"data":[{"id":2,"children":[{"code":"plain","stock":null}]}]}`)
		case "/shared/commodity/inventory":
			if r.Form.Get("sharedCode") == "plain" {
				fmt.Fprint(w, `{"code":200,"data":{"count":22,"delivery_way":0,"draft_status":0,"config":"","is_category":false,"price":999,"factory_price":"7.25"}}`)
			} else {
				count := 100 // The default race only, NEVER the total.
				if r.Form.Get("race") == "A" {
					count = 3
				}
				if r.Form.Get("race") == "B" {
					count = 5
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"count": count, "delivery_way": 0, "draft_status": 0, "is_category": true, "factory_price": 0, "config": "[category]\nA=50\nB=80\n[category_factory]\nA=8.25\nB=12.50"}})
			}
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	for i := 0; i < 2; i++ {
		got, err := a.QuoteProduct(context.Background(), &Product{ID: "plain", Price: 99900, FactoryPrice: 100, IsActive: true})
		if err != nil || got.Price != 725 || got.FactoryPrice != 725 {
			t.Fatalf("plain quote=%+v err=%v", got, err)
		}
	}
	got, err := a.QuoteProduct(context.Background(), &Product{ID: "races", Price: 99900, IsActive: true})
	if err != nil || len(got.SKUs) != 2 || got.SKUs[0].Price != 825 || got.SKUs[1].Price != 1250 {
		t.Fatalf("race quote=%+v err=%v", got, err)
	}
	if calls["/shared/commodity/valuation"] != 1 {
		t.Fatal("missing quote endpoint probed for every product")
	}
	for _, tc := range []struct {
		code, sku string
		want      int32
	}{{"plain", "", 22}, {"races", "", 8}, {"races", "A|", 3}, {"races", "B|", 5}} {
		n, err := a.GetStock(context.Background(), tc.code, tc.sku)
		if err != nil || n != tc.want {
			t.Fatalf("stock %s/%s=%d err=%v", tc.code, tc.sku, n, err)
		}
	}
	if calls["/shared/commodity/stock"] != 1 || calls["/shared/commodity/item"] != 1 {
		t.Fatal("legacy inventory route was not cached", calls)
	}
}

func TestLegacyQuoteOnlyOnExplicitMissingRoute(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		ok     bool
	}{
		{"http404", 404, "404 Not Found", true},
		{"auth", 401, "404 Not Found", false}, {"forbidden", 403, "404 Not Found", false},
		{"limited", 429, "404 Not Found", false}, {"unavailable", 503, "404 Not Found", false},
		{"waf", 200, "<html>Cloudflare challenge</html>", false},
		{"business404", 404, `{"code":404,"msg":"商品不存在"}`, false},
		{"business", 200, `{"code":0,"msg":"商品不存在"}`, false},
		{"bad_price", 200, `{"code":200,"data":{"price":0}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/shared/commodity/inventory" {
					fmt.Fprint(w, `{"code":200,"data":{"delivery_way":0,"draft_status":0,"config":"","is_category":false,"factory_price":7}}`)
					return
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			got, err := a.QuoteProduct(ctx, &Product{ID: "p", Price: 9900})
			if (err == nil) != tc.ok {
				t.Fatalf("quote=%+v err=%v", got, err)
			}
		})
	}
}

func TestLegacyQuotesRejectIncompletePricing(t *testing.T) {
	for _, body := range []string{
		`{"delivery_way":0,"draft_status":0,"config":"","is_category":false}`,
		`{"delivery_way":0,"draft_status":0,"config":"","is_category":false,"factory_price":null}`,
		`{"delivery_way":0,"draft_status":0,"config":"","is_category":false,"factory_price":0}`,
		`{"delivery_way":0,"draft_status":0,"config":"","is_category":false,"factory_price":"1.234"}`,
		`{"factory_price":7}`, `null`,
		`{"delivery_way":0,"draft_status":0,"config":"[category]","is_category":false,"factory_price":7}`,
		`{"delivery_way":0,"draft_status":0,"config":"","is_category":true,"factory_price":7}`,
		`{"delivery_way":0,"draft_status":0,"config":"[category]\nA=50\nB=80\n[category_factory]\nA=7","is_category":true,"factory_price":7}`,
		`{"delivery_way":0,"draft_status":0,"config":"[sku]\nRegion.CN=2","is_category":false,"factory_price":7}`,
		`{"delivery_way":0,"draft_status":0,"config":"[sku]\nbroken","is_category":false,"factory_price":7}`,
		`{"delivery_way":0,"draft_status":0,"config":"[category]\nA=50\n[category_factory]\nA=7\nA=8","is_category":true,"factory_price":7}`,
	} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/shared/commodity/inventory" {
					fmt.Fprintf(w, `{"code":200,"data":%s}`, body)
					return
				}
				legacyMissingPage(w)
			}))
			defer srv.Close()
			a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			p := &Product{ID: "p", Price: 9900, FactoryPrice: 5500}
			got, err := a.QuoteProduct(context.Background(), p)
			if err == nil || got != nil || p.Price != 9900 {
				t.Fatalf("unsafe price accepted: %+v %v", got, err)
			}
		})
	}
}

func TestLegacyItemTreeMustMatchProduct(t *testing.T) {
	for _, body := range []string{
		`[]`, `[{"children":[{"code":"other","stock":8}]}]`,
		`[{"children":[{"code":"P","stock":8},{"code":"P","stock":9}]}]`,
		`[{"children":[{"code":"P","stock":"broken"}]}]`,
	} {
		t.Run(body, func(t *testing.T) {
			inventoryCalls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/shared/commodity/stock":
					legacyMissingPage(w)
				case "/shared/commodity/item":
					fmt.Fprintf(w, `{"code":200,"data":%s}`, body)
				default:
					inventoryCalls++
					fmt.Fprint(w, `{"code":200,"data":{"count":9,"delivery_way":0}}`)
				}
			}))
			defer srv.Close()
			a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			if n, err := a.GetStock(context.Background(), "P", ""); err == nil || n != -2 || inventoryCalls != 0 {
				t.Fatalf("unsafe tree fallback %d %v calls=%d", n, err, inventoryCalls)
			}
		})
	}
}

func TestLegacyStockAggregateFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  any
		status int
		want   int32
		fail   bool
	}{
		{"zero", 0, 200, 2, false}, {"unlimited", -1, 200, -1, false},
		{"overflow", 2147483647, 200, -2, true}, {"missing", nil, 200, -2, true},
		{"bad", "broken", 200, -2, true}, {"limited", 4, 429, -2, true}, {"failure", 4, 503, -2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				if r.URL.Path != "/shared/commodity/inventory" {
					legacyMissingPage(w)
					return
				}
				var count any = 100
				switch r.Form.Get("race") {
				case "A":
					count = 2
				case "B":
					count = tc.value
					w.WriteHeader(tc.status)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"count": count, "delivery_way": 0, "config": "[category]\nA=1\nB=2"}})
			}))
			defer srv.Close()
			a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			a.stockMode.Store(4)
			n, err := a.GetStock(context.Background(), "P", "")
			if (err != nil) != tc.fail || n != tc.want {
				t.Fatalf("stock=%d err=%v", n, err)
			}
		})
	}
}

func TestLegacyCachedItemDoesNotBlockRaceInventory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/shared/commodity/stock":
			legacyMissingPage(w)
		case "/shared/commodity/item":
			fmt.Fprint(w, `{"code":200,"data":{"stock":8}}`)
		case "/shared/commodity/inventory":
			_ = r.ParseForm()
			if r.Form.Get("race") != "A" {
				t.Error("race lost")
			}
			fmt.Fprint(w, `{"code":200,"data":{"count":3,"delivery_way":0,"config":"[category]\nA=1"}}`)
		}
	}))
	defer srv.Close()
	a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	if n, err := a.GetStock(context.Background(), "P", ""); err != nil || n != 8 {
		t.Fatal(n, err)
	}
	if n, err := a.GetStock(context.Background(), "P", "A|"); err != nil || n != 3 {
		t.Fatal(n, err)
	}
}

func TestQuoteDoesNotDropSnapshotSKUOrMixProtocols(t *testing.T) {
	for _, drop := range []bool{true, false} {
		t.Run(fmt.Sprint(drop), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				if r.URL.Path == "/shared/commodity/inventory" {
					cfg := "[category]\nA=1\nB=2\n[category_factory]\nA=1\nB=2"
					if drop {
						cfg = "[category]\nA=1\n[category_factory]\nA=1"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"delivery_way": 0, "draft_status": 0, "is_category": true, "config": cfg}})
					return
				}
				if r.Form.Get("race") == "A" {
					fmt.Fprint(w, `{"code":200,"data":{"price":1}}`)
				} else {
					legacyMissingPage(w)
				}
			}))
			defer srv.Close()
			a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			p := &Product{ID: "P", Price: 100, SKUs: []SKU{{ID: "A|"}, {ID: "B|"}}}
			if got, err := a.QuoteProduct(context.Background(), p); err == nil || got != nil {
				t.Fatal("incomplete/mixed quote accepted", got)
			}
			if a.legacyQuote.Load() {
				t.Fatal("partial modern quote changed protocol")
			}
		})
	}
}
