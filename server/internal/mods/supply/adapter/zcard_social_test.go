package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
)

func TestZCardSocialHTTPContract(t *testing.T) {
	ctx := context.Background()
	calls := map[string]int{}
	parameters := supplyport.SocialParameters{"link": "https://example.com/?q=a%20b", "comments": "重复\r\n重复\nfinal", "posts": "0"}
	schema := supplyport.SocialSchema{Version: 1, Revision: "schema-17", Purchasable: true}
	order := supplyport.SocialOrder{SupplyOrderID: "opaque-A-order", DownstreamOrderNo: "B-request", Status: "pending", Amount: 101, Charged: true, Snapshot: &supplyport.SocialSnapshot{State: "pending", Version: 1, PaidAmountCents: 101, Currency: "CNY", SettlementState: "paid"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		calls[r.URL.Path]++
		if r.Header.Get("X-Supply-Signature") != ZCardSign("secret", r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Supply-Timestamp"), r.Header.Get("X-Supply-Nonce"), raw) {
			t.Error("signature did not bind transmitted request bytes")
		}
		switch r.URL.Path {
		case "/api/supply/products/17/social/schema":
			_ = json.NewEncoder(w).Encode(schema)
		case "/api/supply/products/17/social/quotes":
			var in struct {
				Parameters     supplyport.SocialParameters `json:"parameters"`
				SchemaRevision string                      `json:"schema_revision"`
			}
			_ = json.Unmarshal(raw, &in)
			if !reflect.DeepEqual(parameters, in.Parameters) || in.SchemaRevision != schema.Revision {
				t.Error("quote input changed")
			}
			_ = json.NewEncoder(w).Encode(supplyport.SocialQuote{QuoteID: "quote-A", ProductID: "17", SchemaRevision: schema.Revision, Parameters: parameters, AmountCents: 101, Currency: "CNY", ExpiresAt: supplyport.Integer(time.Now().Add(time.Minute).Unix())})
		case "/api/supply/social/orders":
			var in supplyport.SocialPurchase
			_ = json.Unmarshal(raw, &in)
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			if !reflect.DeepEqual(parameters, in.Parameters) || in.DownstreamOrderNo != "B-request" || string(fields["max_supply_amount_cents"]) != "101" {
				t.Error("purchase intent changed")
			}
			_ = json.NewEncoder(w).Encode(order)
		case "/api/supply/social/orders/opaque-A-order":
			_ = json.NewEncoder(w).Encode(order)
		case "/api/supply/social/orders/opaque-A-order/actions":
			var in map[string]string
			_ = json.Unmarshal(raw, &in)
			if in["action"] != "cancel" || in["operation_id"] != "operation-B" {
				t.Error("operation identity changed")
			}
			_ = json.NewEncoder(w).Encode(supplyport.SocialOperation{OperationID: "operation-B", Status: "pending"})
		case "/api/supply/social/orders/opaque-A-order/actions/operation-B":
			_ = json.NewEncoder(w).Encode(supplyport.SocialOperation{OperationID: "operation-B", Status: "succeeded"})
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	a := &zCardAdapter{creds: Credentials{APIKey: "key", APISecret: "secret"}, t: newTransportWithClient(srv.URL, []int{0}, nil, srv.Client())}
	if _, err := a.SocialSchema(ctx, "17"); err != nil {
		t.Fatal(err)
	}
	quote, err := a.QuoteSocial(ctx, "17", parameters, "schema-17")
	if err != nil || quote.AmountCents != 101 {
		t.Fatal(quote, err)
	}
	purchase := supplyport.SocialPurchase{QuoteID: quote.QuoteID, ProductID: "17", SchemaRevision: quote.SchemaRevision, Parameters: parameters, DownstreamOrderNo: "B-request", RequiredCapability: supplyport.SocialCapability, MaxSupplyAmountCents: 101, Currency: "CNY"}
	created, err := a.CreateSocial(ctx, purchase)
	if err != nil || created.SupplyOrderID != order.SupplyOrderID {
		t.Fatal(created, err)
	}
	queried, err := a.QuerySocial(ctx, []string{created.SupplyOrderID})
	if err != nil || queried[0].Snapshot.Version != 1 {
		t.Fatal(queried, err)
	}
	if _, err = a.QuerySocial(ctx, []string{created.SupplyOrderID, created.SupplyOrderID}); err == nil {
		t.Fatal("duplicate query IDs accepted")
	}
	op, err := a.ActSocial(ctx, created.SupplyOrderID, "cancel", "operation-B")
	if err != nil || op.Status != "pending" {
		t.Fatal(op, err)
	}
	op, err = a.QuerySocialOperation(ctx, created.SupplyOrderID, "operation-B")
	if err != nil || op.Status != "succeeded" {
		t.Fatal(op, err)
	}
	for path, n := range calls {
		if n != 1 {
			t.Errorf("unexpected replay %s: %d", path, n)
		}
	}
}

func TestZCardSocialMutationNoAutomaticReplay(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(502)
		fmt.Fprint(w, `{"error":"uncertain"}`)
	}))
	defer srv.Close()
	a := &zCardAdapter{t: newTransportWithClient(srv.URL, []int{0, 0}, nil, srv.Client())}
	_, err := a.CreateSocial(context.Background(), supplyport.SocialPurchase{QuoteID: "q", ProductID: "17", SchemaRevision: "rev", Parameters: supplyport.SocialParameters{}, DownstreamOrderNo: "same-request", RequiredCapability: supplyport.SocialCapability, MaxSupplyAmountCents: 100, Currency: "CNY"})
	if err == nil || calls != 1 {
		t.Fatalf("purchase was retried: %d %v", calls, err)
	}
}

func TestZCardSocialChargedRejectedReplay(t *testing.T) {
	good := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ref := "confirmed-reject-refund"
		if !good {
			ref = ""
		}
		_ = json.NewEncoder(w).Encode(supplyport.SocialOrder{SupplyOrderID: "existing-A-task", DownstreamOrderNo: "same-B-request", Status: "rejected", Charged: true, Amount: 100, Snapshot: &supplyport.SocialSnapshot{State: "rejected", Version: 3, PaidAmountCents: 100, RefundedAmountCents: 100, SettlementState: "refunded", RefundReference: ref, Currency: "CNY"}})
	}))
	defer srv.Close()
	a := &zCardAdapter{t: newTransportWithClient(srv.URL, []int{0}, nil, srv.Client())}
	req := supplyport.SocialPurchase{QuoteID: "q", ProductID: "17", SchemaRevision: "v1", DownstreamOrderNo: "same-B-request", RequiredCapability: supplyport.SocialCapability, MaxSupplyAmountCents: 100, Currency: "CNY"}
	if _, err := a.CreateSocial(context.Background(), req); err != nil {
		t.Fatalf("confirmed charged rejection replay discarded: %v", err)
	}
	good = false
	if _, err := a.CreateSocial(context.Background(), req); err == nil {
		t.Fatal("charged rejection without refund evidence accepted")
	}
}
