package adapter

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
)

const hpjConfig = `{"appid":"test-app","appsecret":"test-secret","api_url":"https://8.8.8.8"}`

// Independent implementation of the supplied PHP demo's generate_xh_hash.
func hpjSign(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if key != "hash" && value != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	return fmt.Sprintf("%x", md5.Sum([]byte(strings.Join(parts, "&")+"test-secret")))
}

func hpjCallback() map[string]string {
	return map[string]string{"appid": "test-app", "trade_order_id": "ZPtest", "transaction_id": "T123", "total_fee": "10.50", "status": "OD", "attach": "中文 a&b=1"}
}

func TestXunhupayCallback(t *testing.T) {
	a := NewXunhupay()
	form := hpjCallback()
	// Python hashlib vector: UTF-8 raw values, no URL encoding, empty values skipped.
	form["hash"] = "4d98f4c936806902ca76d7c88b2d44a7"
	form["empty"] = ""
	fact, err := a.VerifyCallback(form, json.RawMessage(hpjConfig))
	if err != nil || !fact.Success || fact.Amount != 1050 || fact.Currency != "CNY" || fact.OrderNo != "ZPtest" || fact.ChannelOrderNo != "T123" {
		t.Fatalf("%+v %v", fact, err)
	}
	for _, tc := range []struct {
		key, value    string
		resign, valid bool
	}{
		{"total_fee", "1.00", false, false}, {"hash", "", false, false},
		{"appid", "wrong", true, false}, {"appid", "", true, true},
		{"transaction_id", "", true, false}, {"trade_order_id", "", true, false},
		{"total_fee", "-1", true, false}, {"total_fee", "1.001", true, false},
		{"total_fee", "10oops", true, false}, {"total_fee", "0", true, false},
		{"total_fee", "9223372036854775808", true, false},
		{"extra", "0", true, true},
	} {
		t.Run(tc.key+"_"+tc.value, func(t *testing.T) {
			v := hpjCallback()
			v["hash"] = hpjSign(v)
			v[tc.key] = tc.value
			if tc.resign {
				v["hash"] = hpjSign(v)
			}
			_, err := a.VerifyCallback(v, json.RawMessage(hpjConfig))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
	for _, status := range []string{"WP", "CD", "RD", "UD", ""} {
		v := hpjCallback()
		v["status"] = status
		v["hash"] = hpjSign(v)
		f, err := a.VerifyCallback(v, json.RawMessage(hpjConfig))
		if err != nil || f.Success {
			t.Fatalf("status %q: %+v %v", status, f, err)
		}
	}
	if _, err := a.VerifyCallback(form, json.RawMessage(`{"appid":"test-app","appsecret":"wrong"}`)); err == nil {
		t.Fatal("wrong secret accepted")
	}
}

type hpjTransport func(*http.Request) (*http.Response, error)

func (f hpjTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestXunhupayCreate(t *testing.T) {
	for _, mode := range []string{"success", "tampered", "unsigned", "missing_qr", "missing_url", "unsafe_url", "gateway_error", "missing_errcode", "nested", "invalid_json", "http_error"} {
		t.Run(mode, func(t *testing.T) {
			a := NewXunhupay()
			a.client = &http.Client{Transport: hpjTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != "POST" || req.URL.Path != "/payment/do.html" || !strings.HasPrefix(req.Header.Get("Content-Type"), "application/json") || req.Header.Get("Referer") != "https://shop.example/" {
					t.Fatalf("bad request: %s %s %+v", req.Method, req.URL.Path, req.Header)
				}
				var p map[string]string
				if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				if p["hash"] != hpjSign(p) || p["total_fee"] != "10.50" || p["trade_order_id"] != "ZPtest" || p["version"] != "1.1" || p["appid"] != "test-app" || len(p["nonce_str"]) != 32 || p["notify_url"] != "https://shop.example/payments/callback/hpj?channel_id=9" || p["return_url"] != "https://shop.example/payment/order" {
					t.Fatal("incorrect signed payload")
				}
				if p["appsecret"] != "" || p["type"] != "" {
					t.Fatal("secret leaked or APPID incorrectly routed")
				}
				v := map[string]string{"errcode": "0", "errmsg": "success!", "openid": "12345", "url": "https://pay.example/mobile?a=1&b=2", "url_qrcode": "https://pay.example/qr.png", "extra": "0"}
				switch mode {
				case "missing_qr":
					delete(v, "url_qrcode")
				case "missing_url":
					delete(v, "url")
				case "unsafe_url":
					v["url"] = "javascript:alert(1)"
				case "gateway_error":
					v["errcode"] = "500"
				case "missing_errcode":
					delete(v, "errcode")
				}
				v["hash"] = hpjSign(v)
				if mode == "tampered" {
					v["url"] = "https://evil.example/"
				}
				if mode == "unsigned" {
					delete(v, "hash")
				}
				body, _ := json.Marshal(v)
				// Official response uses a JSON number for errcode.
				body = []byte(strings.Replace(string(body), `"errcode":"0"`, `"errcode":0`, 1))
				if mode == "nested" {
					body = []byte(`{"errcode":0,"data":{"url":"https://pay.example"}}`)
				}
				if mode == "invalid_json" {
					body = []byte("<html>error</html>")
				}
				code := http.StatusOK
				if mode == "http_error" {
					code = 502
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
			})}
			info, err := a.CreatePayment(context.Background(), port.CreatePaymentRequest{OrderNo: "business-order", GatewayOrderRef: "ZPtest", Amount: 1050, Config: json.RawMessage(hpjConfig), NotifyBaseURL: "https://shop.example/payments/callback/hpj?channel_id=9", ReturnURL: "https://shop.example/payment/order"})
			if mode != "success" {
				if err == nil {
					t.Fatalf("%s accepted", mode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if info.Type != "qrcode" || !strings.Contains(string(info.Payload), `"code_url":"https://pay.example/qr.png"`) || time.Until(info.Deadline) < 4*time.Minute {
				t.Fatalf("bad payment info %+v", info)
			}
		})
	}
}

func TestXunhupayConfig(t *testing.T) {
	for _, cfg := range []string{`{}`, `{"appid":"a"}`, `{"appid":"a","appsecret":" "}`, `{"appid":"a","appsecret":"s","api_url":"ftp://example.com"}`, `{"appid":"a","appsecret":"s","api_url":"https://user:pass@example.com"}`, `{"appid":"a","appsecret":"s","api_url":"https://example.com?secret=s"}`} {
		if err := NewXunhupay().ValidateConfig(json.RawMessage(cfg)); err == nil {
			t.Fatalf("accepted %s", cfg)
		}
	}
	c, err := parseXunhupayConfig(json.RawMessage(`{"appid":"a","appsecret":"s"}`))
	if err != nil || c.APIURL != "https://api.xunhupay.com/payment/do.html" {
		t.Fatalf("%+v %v", c, err)
	}
}
