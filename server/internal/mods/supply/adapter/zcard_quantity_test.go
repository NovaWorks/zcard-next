package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestZCardQuantityConfirmationBindsRequestedAmount(t *testing.T) {
	reply := `{"available":true,"quantity":2}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/supply/products/51/availability" || r.URL.Query().Get("quantity") != "2" || r.Header.Get("X-Supply-Signature") == "" {
			t.Error("unsigned or changed quantity request")
		}
		_, _ = w.Write([]byte(reply))
	}))
	defer server.Close()
	a := &zCardAdapter{protocol: "zcard", creds: Credentials{APIKey: "fixture-key", APISecret: "fixture-secret"}, t: newTransportWithClient(server.URL, nil, nil, server.Client())}
	if available, e := a.CheckQuantity(context.Background(), "51", "", 2); e != nil || !available {
		t.Fatal(available, e)
	}
	reply = `{"available":false,"quantity":2}`
	if available, e := a.CheckQuantity(context.Background(), "51", "", 2); e != nil || available {
		t.Fatal(available, e)
	}
	for _, invalid := range []string{`{"available":true,"quantity":1}`, `{"quantity":2}`, `{"available":"true","quantity":2}`} {
		reply = invalid
		if _, e := a.CheckQuantity(context.Background(), "51", "", 2); e == nil {
			t.Fatal("unbound quantity accepted")
		}
	}
}
func TestZCardDeliveredReceiptAndPendingStates(t *testing.T) {
	state := "fulfilled"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"supply_order_id":"51","status":"` + state + `","amount":100,"fulfillment":{"status":"delivered","cards":["fixture-receipt"]}}`))
	}))
	defer server.Close()
	a := &zCardAdapter{protocol: "zcard", creds: Credentials{APIKey: "fixture-key", APISecret: "fixture-secret"}, t: newTransportWithClient(server.URL, nil, nil, server.Client())}
	for _, tc := range []struct{ raw, want string }{{"fulfilled", "delivered"}, {"fulfilling", "pending"}, {"refunded", "refunded"}, {"canceled", "canceled"}} {
		state = tc.raw
		order, e := a.CreateOrder(context.Background(), CreateOrderReq{ProductCode: "1", Quantity: 1, DownstreamOrderNo: "fixture"})
		if e != nil || order.Status != tc.want {
			t.Fatal(order, e)
		}
		got, e := a.GetOrder(context.Background(), "51")
		if e != nil || got.Status != tc.want {
			t.Fatal(got, e)
		}
		if strings.Contains(tc.raw, "refund") && got.Status == "delivered" {
			t.Fatal("old canceled receipt delivered")
		}
	}
}
