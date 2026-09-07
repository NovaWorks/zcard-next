package adapter

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// The gateway v2.0.0 binds amount as float64 and normalizes numeric fields
// before HMAC verification. A mock accepting strings misses the production bug.
func TestEpusdtV2AmountContract(t *testing.T) {
	for _, tc := range []struct {
		cents            int64
		amount, response string
	}{
		{50, "0.5", `0.5`}, {1000, "10", `10`}, {101, "1.01", `1.01`}, {12345, "123.45", `"123.45"`},
	} {
		t.Run(tc.amount, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fields := decodeEpusdtNumericRequest(t, r)
				if fields["amount"] != tc.amount {
					t.Errorf("amount=%q want %q", fields["amount"], tc.amount)
				}
				// Independent HMAC of the fields decoded as the gateway decodes them.
				keys := make([]string, 0, len(fields))
				for k, v := range fields {
					if k != "signature" && v != "" {
						keys = append(keys, k)
					}
				}
				sort.Strings(keys)
				parts := make([]string, 0, len(keys))
				for _, k := range keys {
					parts = append(parts, k+"="+fields[k])
				}
				mac := hmac.New(sha256.New, []byte("contract-secret"))
				mac.Write([]byte(strings.Join(parts, "&")))
				if fields["signature"] != fmt.Sprintf("%x", mac.Sum(nil)) {
					t.Error("signature differs from gateway numeric canonicalization")
					w.WriteHeader(401)
					return
				}
				fmt.Fprintf(w, `{"status_code":200,"data":{"trade_id":"T1","amount":%s,"payment_url":"https://gateway.example/pay/T1"}}`, tc.response)
			}))
			defer srv.Close()
			cfg, _ := json.Marshal(map[string]string{"api_url": srv.URL, "pid": "1000", "secret_key": "contract-secret"})
			reply, err := NewEpusdt().CreatePayment(context.Background(), port.CreatePaymentRequest{OrderNo: "O1", Amount: money.Cents(tc.cents), Config: cfg})
			if err != nil {
				t.Fatal(err)
			}
			if reply.Type != "redirect" || !strings.Contains(string(reply.Payload), "https://gateway.example/pay/T1") {
				t.Fatalf("missing cashier: %+v", reply)
			}
		})
	}
}

func TestEpusdtGatewayErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, want string
	}{
		{"params", 400, `{"status_code":10009,"message":"failed to parse request params"}`, "10009"},
		{"signature", 401, `{"status_code":401,"message":"signature verification failed"}`, "signature verification failed"},
		{"missing route", 404, `<html>not found</html>`, "GMPay"},
		{"reflected secret", 400, `{"status_code":400,"message":"error contract-secret"}`, "[redacted]"},
		{"business rejection", 200, `{"status_code":10003,"message":"no available wallet address"}`, "no available wallet address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer srv.Close()
			cfg, _ := json.Marshal(map[string]string{"api_url": srv.URL, "pid": "1000", "secret_key": "contract-secret"})
			_, err := NewEpusdt().CreatePayment(context.Background(), port.CreatePaymentRequest{OrderNo: "O1", Amount: 50, Config: cfg})
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "contract-secret") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestEpusdtBaseURLValidation(t *testing.T) {
	for _, tc := range []struct {
		base  string
		valid bool
	}{
		{"https://pay.example.com", true}, {" https://pay.example.com/ ", true}, {"http://127.0.0.1:8000/prefix", true},
		{"pay.example.com", false}, {"https://pay.example.com/payments/gmpay/v1", false},
		{"https://pay.example.com/payments/gmpay/v1/order/create-transaction/", false},
		{"https://pay.example.com/payments/gmpay/v1/config", false}, {"https://pay.example.com?token=abc", false},
	} {
		t.Run(tc.base, func(t *testing.T) {
			cfg, _ := json.Marshal(map[string]string{"api_url": tc.base, "pid": "1000", "secret_key": "s"})
			err := NewEpusdt().ValidateConfig(cfg)
			if (err == nil) != tc.valid {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}
