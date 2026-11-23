package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestZCardCardPurchaseContract(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/supply/products", "/api/supply/products/101":
			if !strings.Contains(strings.Join(r.URL.Query()["capabilities"], ","), "card_purchase.v1") {
				t.Error("missing negotiated capability")
			}
			product := `{"id":"101","name":"Account","price":"700","is_active":true,"stock":-2,"skus":[{"id":"9007199254740993","code":"opaque","name":"Yearly","price":"1200","stock":3,"is_active":true}],"purchase_form":{"fields":[{"key":"f_123_7","type":"text","label":"Account","required":true,"max_length":12}]}}`
			if r.URL.Path == "/api/supply/products" {
				fmt.Fprintf(w, `{"items":[%s],"total":1,"page_size":50}`, product)
			} else {
				fmt.Fprintf(w, `{"product":%s}`, product)
			}
		case "/api/supply/products/101/stock", "/api/supply/products/101/availability":
			if r.URL.Query().Get("sku_id") != "9007199254740993" {
				t.Error("selected SKU lost")
			}
			if strings.HasSuffix(r.URL.Path, "availability") {
				if r.URL.Query().Get("quantity") != "2" {
					t.Error("quantity lost")
				}
				fmt.Fprint(w, `{"available":true,"quantity":2}`)
			} else {
				fmt.Fprint(w, `{"stock":3}`)
			}
		case "/api/supply/orders":
			calls++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["sku_id"] != "9007199254740993" || body["required_capability"] != "card_purchase.v1" || body["manual_form_data"].(map[string]any)["f_123_7"] != "alice" {
				t.Error("purchase binding lost", body)
			}
			fmt.Fprint(w, `{"supply_order_id":"301","amount":"2400","status":"fulfilled","fulfillment":{"status":"delivered","cards":["A","B"]}}`)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
		}
	}))
	defer srv.Close()
	a := &zCardAdapter{creds: Credentials{APIKey: "key", APISecret: "secret"}, t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	ctx := context.Background()
	list, e := a.ListProducts(ctx, 1, 50, true)
	if e != nil {
		t.Fatal(e)
	}
	p := list.Items[0]
	if len(p.SKUs) != 1 || p.SKUs[0].ID != "9007199254740993" || p.SKUs[0].Name != "Yearly" || p.SKUs[0].Price != 1200 || len(p.FormFields) != 1 || p.FormFields[0].MaxLength != 12 {
		t.Fatal("catalog loss", p)
	}
	refreshed, e := a.RefreshProduct(ctx, &Product{ID: "101"})
	if e != nil || len(refreshed.SKUs) != 1 || len(refreshed.FormFields) != 1 {
		t.Fatal("detail lost purchase contract", refreshed, e)
	}
	n, e := a.GetStock(ctx, "101", p.SKUs[0].ID)
	if e != nil || n != 3 {
		t.Fatal(n, e)
	}
	ok, e := a.CheckQuantity(ctx, "101", p.SKUs[0].ID, 2)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	out, e := a.CreateOrder(ctx, CreateOrderReq{ProductCode: "101", UpstreamSKU: p.SKUs[0].ID, Quantity: 2, DownstreamOrderNo: "retail", ManualFormData: map[string]any{"f_123_7": "alice"}})
	if e != nil || out.Amount != 2400 || len(out.Cards) != 2 || calls != 1 {
		t.Fatal(out, e, calls)
	}
}

func TestZCardInactiveSKUIsNotImported(t *testing.T) {
	var row zCardProduct
	if e := json.Unmarshal([]byte(`{"id":"1","is_active":true,"skus":[{"id":"7","is_active":false},{"id":"8","is_active":true,"price":"100","stock":2}]}`), &row); e != nil {
		t.Fatal(e)
	}
	p := row.toProduct()
	if len(p.SKUs) != 1 || p.SKUs[0].ID != "8" || p.UpstreamExtra["sku_catalog_complete"] != true {
		t.Fatal("inactive SKU imported or complete catalog lost", p)
	}
}
