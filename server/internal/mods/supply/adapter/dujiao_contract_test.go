package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Regression fixtures mirror dujiao-next 8ae0e004 upstream_catalog.go.
func dujiaoFixtureAdapter(t *testing.T, payload string) *dujiaoAdapter {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, payload)
	}))
	t.Cleanup(srv.Close)
	return &dujiaoAdapter{protocol: "dujiao_next", t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
}

func TestDujiaoCurrentSKUStock(t *testing.T) {
	a := dujiaoFixtureAdapter(t, `{"ok":true,"product":{"id":7,"is_active":true,"skus":[{"id":71,"is_active":true,"stock_quantity":0},{"id":72,"is_active":true,"stock_quantity":9}]}}`)
	got, err := a.GetStock(context.Background(), "7", "72")
	if err != nil || got != 9 {
		t.Fatalf("SKU 72 has 9 units; adapter returned stock=%d err=%v", got, err)
	}
}

func TestDujiaoInactiveDetail(t *testing.T) {
	a := dujiaoFixtureAdapter(t, `{"ok":true,"product":{"id":7,"is_active":false,"skus":[{"id":72,"is_active":true,"stock_quantity":9}]}}`)
	got, err := a.GetStock(context.Background(), "7", "72")
	if !errors.Is(err, ErrProductUnavailable) {
		t.Fatalf("inactive product must be unavailable; got stock=%d err=%v", got, err)
	}
}

func TestDujiaoCatalogStock(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		want          int32
	}{
		{"all SKUs disabled", `{"id":7,"is_active":true,"skus":[]}`, 0},
		{"another SKU in stock", `{"id":7,"is_active":true,"skus":[{"id":71,"is_active":true,"stock_quantity":0},{"id":72,"is_active":true,"stock_quantity":9}]}`, 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw dujiaoProduct
			if err := json.Unmarshal([]byte(tc.payload), &raw); err != nil {
				t.Fatal(err)
			}
			got := raw.toProduct()
			if got.Stock != tc.want {
				t.Fatalf("catalog stock=%d, expected %d", got.Stock, tc.want)
			}
		})
	}
}

func TestDujiaoRefundStatus(t *testing.T) {
	for _, status := range []string{"refunded", "partially_refunded"} {
		t.Run(status, func(t *testing.T) {
			a := dujiaoFixtureAdapter(t, fmt.Sprintf(`{"ok":true,"order_id":88,"status":%q,"amount":"1.00","fulfillment":{"status":"delivered","payload":"old-delivery"}}`, status))
			got, err := a.GetOrder(context.Background(), "88")
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != status {
				t.Fatalf("upstream status %s became %s", status, got.Status)
			}
			if len(got.Cards) != 0 {
				t.Fatal("refunded order exposed old delivery as a new receipt")
			}
		})
	}
}

func TestDujiaoPurchaseFormCatalogAndSubmission(t *testing.T) {
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"ok":true,"total":1,"items":[{"id":18,"is_active":true,"manual_form_schema":{"fields":[{"key":"x_handle","type":"text","label":{"zh-CN":"客户账号"},"required":true}]},"skus":[{"id":19,"is_active":true,"stock_quantity":3}]}]}`)
		} else {
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
				t.Error(err)
			}
			fmt.Fprint(w, `{"ok":true,"order_id":88,"status":"paid","amount":"1.00"}`)
		}
	}))
	defer srv.Close()
	a := &dujiaoAdapter{protocol: "dujiao_next", t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	list, err := a.ListProducts(context.Background(), 1, 50, true)
	if err != nil || len(list.Items[0].FormFields) != 1 || list.Items[0].FormFields[0].Key != "x_handle" {
		t.Fatalf("form schema lost: %v", err)
	}
	_, err = a.CreateOrder(context.Background(), CreateOrderReq{ProductCode: "18", UpstreamSKU: "19", Quantity: 1, DownstreamOrderNo: "fixture-order", ManualFormData: map[string]any{"x_handle": "@alice", "choices": []string{"A", "B"}, "count": 2}})
	if err != nil {
		t.Fatal(err)
	}
	form := sent["manual_form_data"].(map[string]any)
	if form["x_handle"] != "@alice" || form["count"] != float64(2) || len(form["choices"].([]any)) != 2 || sent["sku_id"] != float64(19) {
		t.Fatal("supplier form values or selected SKU changed during serialization")
	}
}

func TestDujiaoUnknownStockCannotBecomeZero(t *testing.T) {
	for _, product := range []string{
		`{"id":7,"is_active":true,"skus":[{"id":72,"is_active":true}]}`,
		`{"id":7,"is_active":true,"skus":[{"id":72,"is_active":true,"stock_quantity":-2}]}`,
		`{"id":7,"is_active":true}`,
	} {
		a := dujiaoFixtureAdapter(t, `{"ok":true,"product":`+product+`}`)
		if stock, err := a.GetStock(context.Background(), "7", ""); err == nil || stock >= -1 {
			t.Fatalf("malformed stock became availability: %d %v", stock, err)
		}
	}
}

func TestDujiaoDeletedDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"ok":false,"error_code":"product_not_found","error_message":"product not found"}`)
	}))
	defer srv.Close()
	a := &dujiaoAdapter{protocol: "dujiao_next", t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	if _, err := a.GetStock(context.Background(), "7", "72"); !errors.Is(err, ErrProductDeleted) {
		t.Fatalf("deleted product classification: %v", err)
	}
}
