package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/cardpurchase"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestZCardDynamicCardQuoteAndSelectionContract(t *testing.T) {
	const sku = "9007199254740993"
	const selection = "opaque-selection+42"
	form := map[string]any{"account": "alice", "password": " keep spaces ", "notes": "first\n\nfirst"}
	quotes, purchases, pings := 0, 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw, _ := io.ReadAll(r.Body)
		wantSignature := ZCardSign("secret", r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Supply-Timestamp"), r.Header.Get("X-Supply-Nonce"), raw)
		if r.Header.Get("X-Supply-Key") != "key" || r.Header.Get("X-Supply-Signature") != wantSignature {
			t.Error("card request lost signed account identity")
		}
		var body map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
		}
		switch r.URL.Path {
		case "/api/supply/ping":
			pings++
			fmt.Fprint(w, `{"ok":true,"currency":"AUD","balance":"50000"}`)
		case "/api/supply/products":
			if !strings.Contains(strings.Join(r.URL.Query()["capabilities"], ","), "card_purchase.v2") {
				t.Error("dynamic card capability was not negotiated")
			}
			fmt.Fprint(w, `{"items":[{"id":"101","name":"Dynamic card","price":"100","is_active":true,"stock":1000,"card_purchase":{"minimum":5,"maximum":0,"dynamic_quote":true,"preselection":true}}],"total":1,"page_size":50}`)
		case "/api/supply/products/101/card/quotes":
			quotes++
			if body["sku_id"] != sku || body["quantity"] != float64(125) || body["card_selection"] != "" {
				t.Error("actual quantity/SKU changed", body)
			}
			answers := body["manual_form_data"].(map[string]any)
			if answers["password"] != form["password"] || answers["notes"] != form["notes"] {
				t.Error("form whitespace or duplicate lines changed", answers)
			}
			fmt.Fprintf(w, `{"quote_id":"quote-125","amount_cents":"12345","premium_cents":"0","quantity":125,"currency":"AUD","expires_at":"%d"}`, time.Now().Add(time.Minute).Unix())
		case "/api/supply/products/101/card/drafts":
			if body["sku_id"] != sku || body["page"] != float64(1) || body["limit"] != float64(20) {
				t.Error("selection paging/SKU changed", body)
			}
			fmt.Fprintf(w, `{"items":[{"selection":%q,"preview":"Account ***42","premium_cents":"37"}],"total":1}`, selection)
		case "/api/supply/orders":
			purchases++
			if body["card_quote_id"] != "quote-125" || body["required_capability"] != "card_purchase.v2" || body["sku_id"] != sku || body["quantity"] != float64(125) || body["max_supply_amount_cents"] != float64(12345) || body["currency"] != "AUD" {
				t.Error("frozen dynamic purchase changed", body)
			}
			fmt.Fprint(w, `{"supply_order_id":"201","status":"pending","amount":"12345","charged":true}`)
		default:
			t.Error("unexpected card endpoint", r.URL.Path)
		}
	}))
	defer srv.Close()
	a := &zCardAdapter{creds: Credentials{APIKey: "key", APISecret: "secret"}, t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
	ctx := context.Background()
	list, err := a.ListProducts(ctx, 1, 50, true)
	if err != nil || len(list.Items) != 1 || list.Items[0].CardRules == nil || list.Items[0].CardRules.Minimum != 5 || list.Items[0].CardRules.Maximum != 0 || !list.Items[0].CardRules.DynamicQuote {
		t.Fatal("quantity rules did not survive the catalog", list, err)
	}
	quote, err := a.QuoteCard(ctx, CardQuoteRequest{ProductCode: "101", UpstreamSKU: sku, Quantity: 125, ManualFormData: form})
	if err != nil || quote.TotalCents != 12345 || quote.Currency != "AUD" || quote.QuoteID != "quote-125" || quote.ExpiresAt <= time.Now().Unix() || quotes != 1 || purchases != 0 || pings != 1 {
		t.Fatal("actual total was rounded, relabeled, or purchased during quotation", quote, err)
	}
	picks, err := a.ListCardPicks(ctx, "101", sku, 1, 20)
	if err != nil || len(picks.Items) != 1 || picks.Items[0].ID != selection || picks.Items[0].PremiumCents != 37 {
		t.Fatal(picks, err)
	}
	if _, err = a.GetCardPick(ctx, "101", sku, selection); !errors.Is(err, ErrNotSupported) {
		t.Fatal("opaque card lookup must be validated through the full quote", err)
	}
	if _, err = a.CreateOrder(ctx, CreateOrderReq{ProductCode: "101", Quantity: 1, CardID: selection}); err == nil || purchases != 0 {
		t.Fatal("selected card was purchased without a supplier quote")
	}
	if _, err = a.CreateOrder(ctx, CreateOrderReq{ProductCode: "101", UpstreamSKU: sku, Quantity: 125, ManualFormData: form, CardQuoteID: quote.QuoteID, CardMaxAmountCents: 12345, CardCurrency: "AUD", DownstreamOrderNo: "stable-order"}); err != nil || purchases != 1 {
		t.Fatal("frozen quote purchase failed", err)
	}
}

func TestZCardCardQuoteRejectsStaleOrMismatchedReceipt(t *testing.T) {
	for _, scenario := range []string{"currency", "quantity", "expired", "missing_amount", "premium", "missing_id"} {
		t.Run(scenario, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/supply/ping" {
					fmt.Fprint(w, `{"ok":true,"currency":"AUD"}`)
					return
				}
				reply := map[string]any{"quote_id": "quote", "amount_cents": 123, "premium_cents": 0, "quantity": 1, "currency": "AUD", "expires_at": time.Now().Add(time.Minute).Unix()}
				switch scenario {
				case "currency":
					reply["currency"] = "USD"
				case "quantity":
					reply["quantity"] = 2
				case "expired":
					reply["expires_at"] = time.Now().Add(-time.Second).Unix()
				case "missing_amount":
					delete(reply, "amount_cents")
				case "premium":
					reply["premium_cents"] = 124
				case "missing_id":
					delete(reply, "quote_id")
				}
				_ = json.NewEncoder(w).Encode(reply)
			}))
			defer srv.Close()
			a := &zCardAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			if result, err := a.QuoteCard(context.Background(), CardQuoteRequest{ProductCode: "101", Quantity: 1}); err == nil || result != nil {
				t.Fatal("invalid supplier quote was accepted", result, err)
			}
		})
	}
}

func TestZCardCardPurchaseDefinitiveRefundReceipts(t *testing.T) {
	for _, tc := range []struct {
		name, status, code string
		amount             int64
		charged            bool
		id                 string
		want               error
	}{
		{"uncharged_quote_expired", "rejected", "card_quote_expired", 0, false, "42", ErrProductUnavailable},
		{"uncharged_price_changed", "rejected", "price_changed", 0, false, "42", ErrProductUnavailable},
		{"charged_then_refunded", "refunded", "card_quote_changed", 12345, true, "42", ErrProductUnavailable},
		{"no_stock_refunded", "refunded", "no_stock", 12345, true, "42", ErrNoStock},
		{"missing_receipt", "refunded", "card_quote_changed", 12345, true, "", ErrPurchaseUncertain},
		{"wrong_amount", "refunded", "card_quote_changed", 12344, true, "42", ErrPurchaseUncertain},
		{"charged_rejected", "rejected", "price_changed", 12345, true, "42", ErrPurchaseUncertain},
		{"unknown_reason", "refunded", "unknown", 12345, true, "42", ErrPurchaseUncertain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"supply_order_id": tc.id, "status": tc.status, "error_code": tc.code, "amount": tc.amount, "charged": tc.charged})
			}))
			defer srv.Close()
			a := &zCardAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			_, err := a.CreateOrder(context.Background(), CreateOrderReq{ProductCode: "101", Quantity: 125, CardQuoteID: "frozen", CardMaxAmountCents: 12345, CardCurrency: "AUD"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("receipt classified incorrectly: %v", err)
			}

			if tc.want != ErrPurchaseUncertain {
				var proof *supplyport.CardPurchaseRejection
				if !errors.As(err, &proof) || proof.QuoteID != "frozen" || proof.ReceiptID != tc.id || proof.Currency != "AUD" || proof.AmountCents != tc.amount || proof.Charged != tc.charged || proof.Status != tc.status || proof.ErrorCode != tc.code {
					t.Fatalf("rejection lost frozen evidence: %v", err)
				}
				if !strings.Contains(err.Error(), tc.id) {
					t.Fatal("audit lost supplier receipt identity")
				}
			}
		})
	}
}

func TestZCardV2EmptyFormClearsRemovedControls(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage(`{"fields":[]}`)} {
		p := zCardProduct{ID: "1", IsActive: true, CardRules: &cardpurchase.Rules{Minimum: 1, DynamicQuote: true}, PurchaseForm: raw}
		got := p.toProduct()
		if !got.IsActive || got.FormFields == nil || len(got.FormFields) != 0 {
			t.Fatalf("v2 omitted form did not clear removed fields: %+v", got)
		}
	}
	legacy := zCardProduct{ID: "2", IsActive: true}.toProduct()
	if legacy.FormFields != nil {
		t.Fatal("legacy unknown schema was converted into a deletion")
	}
	malformed := zCardProduct{ID: "3", IsActive: true, CardRules: &cardpurchase.Rules{Minimum: 1, DynamicQuote: true}, PurchaseForm: json.RawMessage(`{"fields":[{"key":"bad","type":"script"}]}`)}.toProduct()
	if malformed.IsActive {
		t.Fatal("malformed v2 form became purchasable")
	}
}
