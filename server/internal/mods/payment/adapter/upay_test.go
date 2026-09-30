package adapter

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
)

func upayTestConfig(gateway string) json.RawMessage {
	b, _ := json.Marshal(UpayConfig{APIURL: gateway, SecretKey: "test-secret", TradeType: "USDT-TRC20"})
	return b
}

// Independent fixture mirrors upstream fixed-format signing, not adapter helpers.
func upayNotification(amount, actual any, block string, status int) []byte {
	m := map[string]any{"trade_id": "T1", "order_id": "UP1", "amount": amount, "actual_amount": actual, "token": "T-wallet", "block_transaction_id": block, "status": status}
	a, _ := json.Marshal(amount)
	b, _ := json.Marshal(actual)
	var af, bf float64
	_ = json.Unmarshal(a, &af)
	_ = json.Unmarshal(b, &bf)
	sign := fmt.Sprintf("actual_amount=%g&amount=%g&block_transaction_id=%s&order_id=UP1&status=%d&token=T-wallet&trade_id=T1test-secret", bf, af, block, status)
	m["signature"] = fmt.Sprintf("%x", md5.Sum([]byte(sign)))
	out, _ := json.Marshal(m)
	return out
}

func TestUpayCreateContract(t *testing.T) {
	for _, trade := range upayTrades {
		t.Run(trade.Value, func(t *testing.T) {
			for _, millis := range []bool{true, false} {
				expires := time.Now().Add(5 * time.Minute).Truncate(time.Second)
				gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "POST" || r.URL.Path != "/api/create_order" || r.Header.Get("Content-Type") != "application/json" {
						t.Error("wrong endpoint or content type")
					}
					var m map[string]any
					if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
						t.Error(err)
					}
					base := fmt.Sprintf("amount=10.5&notify_url=https://shop.test/payments/callback/upay?channel_id=7&order_id=UP1&redirect_url=https://shop.test/payment/S1&type=%stest-secret", trade.Value)
					if m["signature"] != fmt.Sprintf("%x", md5.Sum([]byte(base))) || m["type"] != trade.Value || m["amount"] != 10.5 {
						t.Errorf("request contract mismatch: %v", m)
					}
					stamp := expires.Unix()
					if millis {
						stamp = expires.UnixMilli()
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "data": map[string]any{"trade_id": "T1", "order_id": "UP1", "amount": 10.5, "actual_amount": 1.5001, "token": "T-wallet", "expiration_time": stamp, "payment_url": "https://pay.test/pay/checkout-counter/T1"}})
				}))
				cfg, _ := json.Marshal(UpayConfig{APIURL: gateway.URL, SecretKey: "test-secret", TradeType: trade.Value})
				info, err := NewUpay().CreatePayment(context.Background(), port.CreatePaymentRequest{Config: cfg, GatewayOrderRef: "UP1", Amount: 1050, Deadline: time.Now().Add(20 * time.Minute), NotifyBaseURL: "https://shop.test/payments/callback/upay?channel_id=7", ReturnURL: "https://shop.test/payment/S1"})
				gateway.Close()
				if err != nil {
					t.Fatal(err)
				}
				if info.Type != "redirect" || info.ChannelOrderNo != "T1" || !info.Deadline.Equal(expires) || string(info.Payload) != "https://pay.test/pay/checkout-counter/T1" {
					t.Fatalf("wrong checkout: %+v", info)
				}
			}
		})
	}
}

func TestUpayCallbackContract(t *testing.T) {
	p := NewUpay()
	cfg := upayTestConfig("https://pay.test")
	for _, block := range []string{"", "0", "chain-hash"} {
		for _, status := range []int{1, 2, 3} {
			fact, err := p.ParseWebhook(nil, upayNotification(10.50, 1.5001, block, status), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if fact.Amount != 1050 || fact.Currency != "CNY" || fact.GatewayOrderRef != "UP1" || fact.ChannelOrderNo != "T1" || fact.Success != (status == 2) {
				t.Fatalf("wrong fact: %+v", fact)
			}
		}
	}
	for _, tc := range []struct {
		key   string
		value any
	}{
		{"signature", "bad"}, {"amount", 10.51}, {"actual_amount", 2.0}, {"token", "other"}, {"order_id", "other"}, {"trade_id", "other"}, {"block_transaction_id", "other"}, {"status", 1},
	} {
		t.Run(tc.key, func(t *testing.T) {
			var m map[string]any
			_ = json.Unmarshal(upayNotification(10.50, 1.5001, "0", 2), &m)
			m[tc.key] = tc.value
			body, _ := json.Marshal(m)
			if _, err := p.ParseWebhook(nil, body, cfg); err == nil {
				t.Fatal("tampered callback accepted")
			}
		})
	}
	for _, amount := range []any{0, -1, 10.001, 1e16, "1e999999999", "NaN", "10junk"} {
		if _, err := p.ParseWebhook(nil, upayNotification(amount, 1, "0", 2), cfg); err == nil {
			t.Fatalf("invalid amount accepted %v", amount)
		}
	}
	if _, err := p.ParseWebhook(nil, []byte(`{}`), cfg); err == nil {
		t.Fatal("empty callback accepted")
	}
}

func TestUpayConfigAndResponseRejection(t *testing.T) {
	for _, patch := range []map[string]any{{"api_url": "file:///tmp/a"}, {"api_url": "https://a:b@pay.test"}, {"api_url": "https://pay.test?key=a"}, {"secret_key": " "}, {"trade_type": "usdt.trc20"}, {"timeout": 59}, {"timeout": 3601}, {"currency": "USD"}} {
		m := map[string]any{"api_url": "https://pay.test", "secret_key": "test-secret"}
		for k, v := range patch {
			m[k] = v
		}
		b, _ := json.Marshal(m)
		if err := NewUpay().ValidateConfig(b); err == nil {
			t.Fatalf("invalid config accepted %v", patch)
		}
	}
	for _, patch := range []map[string]any{{"order_id": "other"}, {"trade_id": ""}, {"amount": 9.0}, {"amount": 10.001}, {"actual_amount": 0}, {"token": ""}, {"expiration_time": time.Now().Add(-time.Minute).UnixMilli()}, {"payment_url": "javascript:alert(1)"}} {
		gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			d := map[string]any{"trade_id": "T1", "order_id": "UP1", "amount": 10, "actual_amount": 1.5, "token": "wallet", "expiration_time": time.Now().Add(time.Minute).UnixMilli(), "payment_url": "https://pay.test/pay/T1"}
			for k, v := range patch {
				d[k] = v
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "data": d})
		}))
		_, err := NewUpay().CreatePayment(context.Background(), port.CreatePaymentRequest{Config: upayTestConfig(gateway.URL), GatewayOrderRef: "UP1", Amount: 1000, Deadline: time.Now().Add(time.Minute), NotifyBaseURL: "https://shop.test/notify", ReturnURL: "https://shop.test/return"})
		gateway.Close()
		if err == nil {
			t.Fatalf("invalid response accepted %v", patch)
		}
	}
	for _, response := range []string{`{"status_code":401,"message":"test-secret"}`, `{"code":1,"message":"test-secret"}`, "not json"} {
		gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, response) }))
		_, err := NewUpay().CreatePayment(context.Background(), port.CreatePaymentRequest{Config: upayTestConfig(gateway.URL), GatewayOrderRef: "UP1", Amount: 1000, Deadline: time.Now().Add(time.Minute), NotifyBaseURL: "https://shop.test/notify", ReturnURL: "https://shop.test/return"})
		gateway.Close()
		if err == nil || strings.Contains(err.Error(), "test-secret") {
			t.Fatalf("unsafe error %v", err)
		}
	}
}
