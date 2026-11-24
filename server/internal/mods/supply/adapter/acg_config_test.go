package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAcgConfigFormatsPreserveSpecsAndAccountPrices(t *testing.T) {
	for _, raw := range []string{`""`, `[]`, `{}`, `null`} {
		var config acgConfig
		if err := json.Unmarshal([]byte(raw), &config); err != nil || config != "" {
			t.Fatalf("empty PHP config %s: %q %v", raw, config, err)
		}
	}
	raw := `{"category":{"B":"20.00","A":10},"category_factory":{"B":"8.25","A":"4.50"},"sku":{"Region":{"US":"2.00","CN":0}},"wholesale":{"10":"9.00"}}`
	var config acgConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	ini, err := parseAcgINI(string(config))
	if err != nil {
		t.Fatal(err)
	}
	combos, err := buildAcgCombos(ini, 1000)
	if err != nil || len(combos) != 4 || combos[0].Code != "A|Region=CN" || combos[3].Code != "B|Region=US" {
		t.Fatalf("config lost dimensions: %q %+v %v", config, combos, err)
	}
	var flat acgConfig
	if err := json.Unmarshal([]byte(`{"sku":{"Region.CN":0,"Region.US":"2.00"}}`), &flat); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(flat), "Region.CN=0\nRegion.US=2.00") {
		t.Fatal(flat)
	}
	var inv acgInventory
	inv.raw = []byte(`{"delivery_way":0,"draft_status":0,"is_category":true,"config":{"category":{"A":"10","B":"20"},"category_factory":{"A":"4.50","B":"8.25"}}}`)
	prices, err := legacyAccountPrices(&inv)
	if err != nil || prices["A"] != 450 || prices["B"] != 825 {
		t.Fatalf("account prices: %v %v", prices, err)
	}
	inv.raw = []byte(`{"delivery_way":0,"draft_status":0,"is_category":false,"factory_price":"3.25","config":[]}`)
	prices, err = legacyAccountPrices(&inv)
	if err != nil || prices[""] != 325 {
		t.Fatalf("legacy empty PHP array: %v %v", prices, err)
	}
	// The canonical race-only code has a trailing separator, which is valid.
	if fields, err := AcgSpecFormFields("美国|"); err != nil || fields["race"] != "美国" {
		t.Fatalf("valid race code rejected: %v %v", fields, err)
	}
}

func TestAcgConfigRejectsAmbiguousShapes(t *testing.T) {
	for _, raw := range []string{
		`["not empty"]`, `1`, `true`, `{"category":[]}`, `{"category":{}}`,
		`{"category":{"A":null}}`, `{"category":{"A":true}}`,
		`{"category":{"A":{"nested":"1"}}}`, `{"sku":{"Region":1}}`,
		`{"category":{"A":"1","A":"2"}}`,
		`{"sku":{"Region":{"CN":0},"Region.CN":2}}`,
		`{"category":{"A\nB":"1"}}`, `{"category":{"A":"1\nB=2"}}`,
		`{"unknown_spec":{"A":1}}`,
	} {
		var config acgConfig
		if err := json.Unmarshal([]byte(raw), &config); err == nil {
			t.Errorf("ambiguous config accepted: %s => %q", raw, config)
		}
	}
}

func TestAcgObjectConfigAcrossCatalogQuoteAndLegacyStock(t *testing.T) {
	config := map[string]any{"category": map[string]string{"A": "10", "B": "20"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/shared/commodity/items":
			json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": []any{map[string]any{"id": 1, "children": []any{map[string]any{"code": "P", "price": 10, "stock": 999, "shared_sync": 1, "config": config}}}}})
		case "/shared/commodity/inventory":
			count := 10
			if r.Form.Get("race") == "B" {
				count = 20
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"delivery_way": 0, "draft_status": 0, "is_category": true, "count": count, "config": config}})
		case "/shared/commodity/valuation":
			price := "4.50"
			if r.Form.Get("race") == "B" {
				price = "8.25"
			}
			fmt.Fprintf(w, `{"code":200,"data":{"price":%q}}`, price)
		default:
			w.WriteHeader(404) // Old sites lack stock/item routes.
		}
	}))
	defer srv.Close()
	a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	list, err := a.ListProducts(context.Background(), 1, 20, false)
	if err != nil || len(list.Items) != 1 || len(list.Items[0].SKUs) != 2 || list.Items[0].Stock != 999 {
		t.Fatalf("catalog: %+v %v", list, err)
	}
	quoted, err := a.QuoteProduct(context.Background(), &list.Items[0])
	if err != nil || quoted.FactoryPrice != 450 || quoted.SKUs[1].Price != 825 {
		t.Fatalf("quote: %+v %v", quoted, err)
	}
	stock, err := a.GetStock(context.Background(), "P", "")
	if err != nil || stock != 30 {
		t.Fatalf("legacy aggregate stock: %d %v", stock, err)
	}
}

func TestAcgReadFailureClassificationAndCatalogStop(t *testing.T) {
	for _, tc := range []struct {
		message string
		want    error
	}{
		{"服务器频繁请联系客服支持", ErrRateLimited},
		{"当前商品已停售", ErrProductUnavailable},
		{"商品不存在", ErrProductDeleted},
	} {
		body, _ := json.Marshal(map[string]any{"code": 500, "msg": tc.message})
		if _, err := parseAcgReadResp(body); !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v", tc.message, err)
		}
		if _, err := parseResp(body); errors.Is(err, tc.want) {
			t.Fatal("read normalization leaked into purchase response handling")
		}
	}
	_, err := parseAcgReadResp([]byte(`{"code":403,"msg":"请求过于频繁"}`))
	if !ClassifyImportError(err).Pause || errors.Is(err, ErrRateLimited) {
		t.Fatalf("authentication error reclassified: %v", err)
	}
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shared/commodity/items" {
			fmt.Fprint(w, `{"code":200,"data":[{"id":1,"children":[{"code":"A","price":1},{"code":"B","price":1}]}]}`)
			return
		}
		reads++
		fmt.Fprint(w, `{"code":500,"msg":"服务器频繁请联系客服支持"}`)
	}))
	defer srv.Close()
	a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	if _, err := a.ListProducts(context.Background(), 1, 20, false); !errors.Is(err, ErrRateLimited) || reads != 1 {
		t.Fatalf("catalog continued after rate limit: reads=%d err=%v", reads, err)
	}
}
