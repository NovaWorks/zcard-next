package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestZCardRetryResignsEveryTransmission(t *testing.T) {
	seen := map[string]bool{}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		nonce := r.Header.Get("X-Supply-Nonce")
		if seen[nonce] {
			t.Error("nonce replayed")
		}
		seen[nonce] = true
		expected := ZCardSign("secret", r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Supply-Timestamp"), nonce, raw)
		if r.Header.Get("X-Supply-Signature") != expected {
			t.Error("signature mismatch")
		}
		if calls == 1 {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"reason":"temporary"}`)
			return
		}
		fmt.Fprint(w, `{"ok":true,"balance":"9007199254740993","currency":"CNY","capabilities":["sms_activation.v1","sms_polling.v1"]}`)
	}))
	defer srv.Close()
	a := &zCardAdapter{creds: Credentials{APIKey: "key", APISecret: "secret"}, t: newTransportWithClient(srv.URL, []int{0}, nil, srv.Client())}
	out, err := a.Ping(context.Background())
	if err != nil || out.Balance != 9007199254740993 || len(out.Capabilities) != 2 || calls != 2 {
		t.Fatalf("ping precision/retry failed: %+v %v %d", out, err, calls)
	}
}
func TestZCardSMSPublicContractTwoSources(t *testing.T) {
	// Same client, endpoint and capability; a new normalized source is added at runtime.
	source := "A"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/supply/products":
			if r.URL.Query().Get("capabilities") != supplyport.SMSCapability || r.URL.Query().Get("page") != "2001" {
				t.Error("catalog capability or high page absent")
			}
			fmt.Fprint(w, `{"items":[{"id":9007199254740993,"name":"opaque service","price":"101","delivery_kind":"sms_activation","sms_product":{"country_id":"optional-id","future_key":"ignored"}}],"total":"100001","page_size":50,"has_more":false}`)
		case "/api/supply/orders":
			calls++
			raw, _ := io.ReadAll(r.Body)
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			if _, ok := fields["callback_url"]; ok {
				t.Error("SMS callback sent")
			}
			if string(fields["max_supply_amount_cents"]) != `"101"` {
				t.Error("price ceiling not integer string")
			}
			if source == "reject" {
				fmt.Fprint(w, `{"supply_order_id":"r1","status":"rejected","error_code":"price_changed"}`)
				return
			}
			fmt.Fprintf(w, `{"supply_order_id":%q,"charged":true,"amount":"101","status":"fulfilling","fulfillment":{"kind":"sms_activation","sms":{"session_id":"opaque","version":"9007199254740993","state":"allocating","currency":"CNY","paid_amount_cents":"101","settlement_state":"paid"}},"new_optional":"compatible"}`, source)
		case "/api/supply/sms/sessions/query":
			fmt.Fprintf(w, `{"orders":[{"supply_order_id":%q,"downstream_order_no":"sms_fixed","amount":"101","fulfillment":{"kind":"sms_activation","sms":{"session_id":"opaque","version":"9007199254740994","sms_revision":1,"state":"sms_received","phone_number":"+00 12-XY","otp_code":"00AB","otp_message":"正文只有文字也可交付","currency":"CNY","paid_amount_cents":"101","settlement_state":"paid","can_cancel":false,"can_finish":false}}}]}`, source)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	a := &zCardAdapter{t: newTransportWithClient(srv.URL, []int{0}, nil, srv.Client())}
	ctx := context.Background()
	list, err := a.ListProducts(ctx, 2001, 50, true)
	if err != nil || list.Items[0].ID != "9007199254740993" || list.Total != 100001 || list.HasMore || len(list.Items[0].SMSProduct) != 1 {
		t.Fatalf("catalog: %+v %v", list, err)
	}
	req := supplyport.SMSPurchase{ProductID: list.Items[0].ID, Quantity: 1, DownstreamOrderNo: "sms_fixed", RequiredCapability: supplyport.SMSCapability, MaxSupplyAmountCents: 101, Currency: "CNY"}
	for _, provider := range []string{"A", "B-string-order"} {
		source = provider
		o, e := a.CreateSMS(ctx, req)
		if e != nil {
			t.Fatal(e)
		}
		if o.Fulfillment.SMS.Version != 9007199254740993 {
			t.Fatal("version precision lost")
		}
		rows, e := a.QuerySMS(ctx, []string{o.SupplyOrderID})
		if e != nil || rows[0].Fulfillment.SMS.OTPCode != "00AB" {
			t.Fatalf("generic source contract: %v", e)
		}
	}
	source = "reject"
	o, err := a.CreateSMS(ctx, req)
	if err != nil || o.Charged || o.Fulfillment != nil || o.Status != "rejected" {
		t.Fatalf("omitted protobuf defaults %v %v", o, err)
	}
	req.Quantity = 2
	if _, err = a.CreateSMS(ctx, req); err == nil {
		t.Fatal("quantity allowed")
	}
	if calls != 3 {
		t.Fatal("invalid purchase reached upstream")
	}
}
func TestProtocolIntegerRejectsLossyForms(t *testing.T) {
	for _, raw := range []string{`1.2`, `1e3`, `null`, `true`, `"9223372036854775808"`} {
		var n supplyport.Integer
		if json.Unmarshal([]byte(raw), &n) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestZCardSMSUnknownOutcomeAndConflicts(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(409)
		fmt.Fprint(w, `{"reason":"supply.IDEMPOTENCY_CONFLICT","message":"do not retry with new identity"}`)
	}))
	defer srv.Close()
	a := &zCardAdapter{t: newTransportWithClient(srv.URL, []int{0, 0}, nil, srv.Client())}
	_, err := a.CreateSMS(context.Background(), supplyport.SMSPurchase{ProductID: "1", Quantity: 1, DownstreamOrderNo: "fixed", RequiredCapability: supplyport.SMSCapability, MaxSupplyAmountCents: 1, Currency: "CNY"})
	if err == nil || calls != 1 || upstreamErrorCode(err) != "supply.IDEMPOTENCY_CONFLICT" {
		t.Fatal("conflict not preserved")
	}
}
