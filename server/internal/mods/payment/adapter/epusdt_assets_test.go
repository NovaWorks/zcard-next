package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
)

func TestEpusdtAssetAvailability(t *testing.T) {
	a := NewEpusdt()
	for _, tc := range []struct {
		network string
		tokens  []string
	}{
		{"tron", []string{"TRX", "USDT"}}, {"polygon", []string{"USDC", "USDT"}},
	} {
		result, err := a.FieldOptions(context.Background(), "token", json.RawMessage(fmt.Sprintf(`{"network":%q}`, tc.network)))
		if err != nil || !result.Fallback || len(result.Options) != len(tc.tokens) {
			t.Fatalf("fallback: %+v %v", result, err)
		}
		for i, token := range tc.tokens {
			if result.Options[i].Value != token {
				t.Fatalf("invalid %s fallback: %+v", tc.network, result.Options)
			}
		}
	}
	for _, tc := range []struct {
		body     string
		fallback bool
		count    int
	}{
		{`{"message":"failed"}`, true, 6},
		{`{"data":{}}`, true, 6},
		{`{"data":{"supported_assets":[]}}`, false, 0},
		{`{"data":{"supported_assets":[{"network":"polygon","tokens":["USDT","POL"]}]}}`, false, 1},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
		cfg, _ := json.Marshal(map[string]string{"api_url": srv.URL})
		result, err := a.FieldOptions(context.Background(), "network", cfg)
		if err != nil || result.Fallback != tc.fallback || len(result.Options) != tc.count {
			t.Fatalf("assets: %+v %v", result, err)
		}
		if tc.count == 1 {
			tokens, err := a.FieldOptions(context.Background(), "token", cfg)
			if err != nil || len(tokens.Options) != 2 || tokens.Options[0].Value != "POL" {
				t.Fatalf("must honor gateway advertised tokens: %+v %v", tokens, err)
			}
		}
		srv.Close()
	}
}

func TestEpusdtMethodAssetRouting(t *testing.T) {
	for _, tc := range []struct{ network, token string }{{"tron", "TRX"}, {"polygon", "USDT"}, {"polygon", "USDC"}} {
		t.Run(tc.network+tc.token, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fields := decodeEpusdtNumericRequest(t, r)
				if fields["network"] != tc.network || fields["token"] != tc.token {
					t.Errorf("wrong asset: %v/%v", fields["network"], fields["token"])
				}
				if !epusdtVerifySign(fields, "fixture-secret", fields["signature"]) {
					t.Error("invalid signature")
				}
				fmt.Fprint(w, `{"status_code":200,"data":{"trade_id":"fixture","amount":10,"payment_url":"https://gateway.example/pay/fixture"}}`)
			}))
			defer srv.Close()
			cfg, _ := json.Marshal(map[string]string{"api_url": srv.URL, "pid": "fixture", "secret_key": "fixture-secret", "network": "tron", "token": "USDT"})
			_, err := NewEpusdt().CreatePayment(context.Background(), port.CreatePaymentRequest{OrderNo: "fixture", Amount: 1000, Config: cfg, MethodParams: map[string]string{"network": tc.network, "token": tc.token}})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
