package adapter

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

func alipayConfigForTest(t *testing.T) json.RawMessage {
	t.Helper()
	cfg, err := json.Marshal(alipayConfig{AppID: "2021001", PrivateKey: alipayTestPriv, AlipayPublicKey: alipayTestPub, Gateway: "https://8.8.8.8/gateway.do"})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestAlipayKeyFormats(t *testing.T) {
	priv, err := parseRSAPrivateKey(alipayTestPriv)
	if err != nil {
		t.Fatal(err)
	}
	pkcs1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}))
	pub1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&priv.PublicKey)}))
	for name, key := range map[string]string{"PKCS8": alipayTestPriv, "PKCS1": pkcs1, "PKIX": alipayTestPub, "public_PKCS1": pub1} {
		t.Run(name, func(t *testing.T) {
			block, _ := pem.Decode([]byte(key))
			values := map[string]string{
				"PEM": key, "raw": base64.StdEncoding.EncodeToString(block.Bytes),
				"collapsed": strings.ReplaceAll(key, "\n", ""), "escaped": strings.ReplaceAll(key, "\n", `\n`),
				"CRLF": strings.ReplaceAll(key, "\n", "\r\n"), "escaped_CRLF": strings.ReplaceAll(key, "\n", `\r\n`),
				"BOM": "\ufeff" + key,
			}
			for format, value := range values {
				t.Run(format, func(t *testing.T) {
					if strings.HasPrefix(name, "public") || name == "PKIX" {
						k, e := parseRSAPublicKey(value)
						if e != nil || k.N.Cmp(priv.N) != 0 {
							t.Fatalf("public key: %v", e)
						}
					} else {
						k, e := parseRSAPrivateKey(value)
						if e != nil || k.N.Cmp(priv.N) != 0 {
							t.Fatalf("private key: %v", e)
						}
					}
				})
			}
		})
	}
	for _, value := range []string{"", "garbage", alipayTestPub, alipayTestPriv + "junk", strings.Replace(alipayTestPriv, "END PRIVATE KEY", "END RSA PRIVATE KEY", 1), strings.ReplaceAll(alipayTestPriv, "PRIVATE KEY", "ENCRYPTED PRIVATE KEY")} {
		if _, err := parseRSAPrivateKey(value); err == nil {
			t.Fatal("invalid private key accepted")
		}
	}
	if _, err := parseRSAPublicKey(strings.ReplaceAll(alipayTestPub, "PUBLIC KEY", "CERTIFICATE")); err == nil || !strings.Contains(err.Error(), "证书") {
		t.Fatal("certificate must have a clear error")
	}
}

// Independent request canonicalization, matching the official SDK: sign_type participates.
func verifyAlipayRequest(t *testing.T, values url.Values) {
	t.Helper()
	keys := make([]string, 0, len(values))
	for k := range values {
		if k != "sign" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+values.Get(k))
	}
	digest := sha256.Sum256([]byte(strings.Join(pairs, "&")))
	sig, err := base64.StdEncoding.DecodeString(values.Get("sign"))
	if err != nil {
		t.Fatal(err)
	}
	pub, err := parseRSAPublicKey(alipayTestPub)
	if err != nil {
		t.Fatal(err)
	}
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("request signature differs from official SDK: %v", err)
	}
	ts, err := time.ParseInLocation("2006-01-02 15:04:05", values.Get("timestamp"), time.FixedZone("China", 8*60*60))
	if err != nil || time.Since(ts).Abs() > 5*time.Second {
		t.Fatalf("timestamp not China time: %s", values.Get("timestamp"))
	}
}

func TestAlipayCreateRequest(t *testing.T) {
	req := port.CreatePaymentRequest{Config: alipayConfigForTest(t), OrderNo: "business-order", GatewayOrderRef: "ZP-attempt", Amount: 1234, Subject: "中文 a&b=1", NotifyBaseURL: "https://example.com/callback?channel_id=2", ReturnURL: "https://example.com/return"}
	info, err := NewAlipay().CreatePayment(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(info.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(payload.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	verifyAlipayRequest(t, q)
	var biz struct {
		OrderNo string `json:"out_trade_no"`
		Amount  string `json:"total_amount"`
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal([]byte(q.Get("biz_content")), &biz); err != nil {
		t.Fatal(err)
	}
	if info.Type != "redirect" || biz.OrderNo != "ZP-attempt" || biz.Amount != "12.34" || biz.Subject != req.Subject || q.Get("notify_url") != req.NotifyBaseURL {
		t.Fatalf("incorrect request: %+v", biz)
	}
	for _, amount := range []int64{0, -1, 1<<63 - 1} {
		req.Amount = money.Cents(amount)
		if _, err := NewAlipay().CreatePayment(context.Background(), req); err == nil {
			t.Fatal("invalid amount accepted")
		}
	}
	req.Amount = 1234
	req.ChargedCurrency = "USD"
	if _, err := NewAlipay().CreatePayment(context.Background(), req); err == nil {
		t.Fatal("foreign currency accepted")
	}
}

func signAlipayCallback(t *testing.T, form map[string]string) {
	t.Helper()
	priv, err := parseRSAPrivateKey(alipayTestPriv)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := rsa2Sign(priv, []byte(sortParams(form, "sign", "sign_type")))
	if err != nil {
		t.Fatal(err)
	}
	form["sign"] = sig
}
func TestAlipayCallbackBinding(t *testing.T) {
	for _, tc := range []struct {
		key, value     string
		valid, success bool
	}{
		{"trade_status", "TRADE_SUCCESS", true, true}, {"trade_status", "TRADE_FINISHED", true, true}, {"trade_status", "TRADE_CLOSED", true, false},
		{"app_id", "other-app", false, false}, {"app_id", "", false, false}, {"sign_type", "RSA", false, false},
		{"out_trade_no", "", false, false}, {"trade_no", "", false, false},
		{"total_amount", "10junk", false, false}, {"total_amount", "10.0x", false, false}, {"total_amount", "0", false, false}, {"total_amount", "-1", false, false},
		{"total_amount", "1.001", false, false}, {"total_amount", "9223372036854775808", false, false},
	} {
		t.Run(tc.key+"_"+tc.value, func(t *testing.T) {
			form := map[string]string{"app_id": "2021001", "sign_type": "RSA2", "out_trade_no": "ZP-attempt", "trade_no": "T123", "total_amount": "10.00", "trade_status": "TRADE_SUCCESS"}
			form[tc.key] = tc.value
			signAlipayCallback(t, form)
			fact, err := NewAlipay().VerifyCallback(form, alipayConfigForTest(t))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.valid && (fact.Success != tc.success || fact.Amount != 1000 || fact.OrderNo != "ZP-attempt") {
				t.Fatalf("incorrect fact: %+v", fact)
			}
			if tc.valid {
				form["total_amount"] = "99.00"
				if _, err := NewAlipay().VerifyCallback(form, alipayConfigForTest(t)); err == nil {
					t.Fatal("tampered notification accepted")
				}
			}
		})
	}
	if NewAlipay().SuccessAck() != "success" {
		t.Fatal("incorrect ACK")
	}
}

type alipayTransport func(*http.Request) (*http.Response, error)

func (f alipayTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAlipayQueryVerification(t *testing.T) {
	for _, mode := range []string{"valid", "whitespace", "unsigned", "tampered", "mismatch", "missing_trade_no", "bad_amount", "zero_amount", "http_error", "error_response", "pending"} {
		t.Run(mode, func(t *testing.T) {
			a := NewAlipay()
			a.client = &http.Client{Transport: alipayTransport(func(r *http.Request) (*http.Response, error) {
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				verifyAlipayRequest(t, r.PostForm)
				var biz map[string]string
				_ = json.Unmarshal([]byte(r.PostForm.Get("biz_content")), &biz)
				if biz["trade_no"] != "T123" || biz["out_trade_no"] != "" {
					t.Fatalf("query uses incorrect identifier: %+v", biz)
				}
				result := map[string]string{"code": "10000", "trade_status": "TRADE_SUCCESS", "trade_no": "T123", "out_trade_no": "ZP-attempt", "total_amount": "10.00"}
				switch mode {
				case "mismatch":
					result["trade_no"] = "other"
				case "missing_trade_no":
					delete(result, "trade_no")
				case "bad_amount":
					result["total_amount"] = "10junk"
				case "zero_amount":
					result["total_amount"] = "0.00"
				case "pending":
					result["trade_status"] = "WAIT_BUYER_PAY"
				}
				raw, _ := json.Marshal(result)
				if mode == "whitespace" {
					raw, _ = json.MarshalIndent(result, "", "  ")
				}
				priv, _ := parseRSAPrivateKey(alipayTestPriv)
				sig, _ := rsa2Sign(priv, raw)
				if mode == "unsigned" {
					sig = ""
				}
				if mode == "tampered" {
					raw = []byte(strings.Replace(string(raw), "10.00", "99.00", 1))
				}
				body := `{"alipay_trade_query_response":` + string(raw) + `,"sign":"` + sig + `"}`
				if mode == "error_response" {
					body = `{"error_response":{"code":"40004","sub_code":"ACQ.TRADE_NOT_EXIST"}}`
				}
				status := 200
				if mode == "http_error" {
					status = 502
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			fact, err := a.QueryPayment(context.Background(), "T123", alipayConfigForTest(t))
			valid := mode == "valid" || mode == "whitespace" || mode == "pending"
			if (err == nil) != valid {
				t.Fatalf("mode %s: %+v %v", mode, fact, err)
			}
			if valid && (fact.Success != (mode != "pending") || fact.Amount != 1000) {
				t.Fatalf("incorrect result: %+v", fact)
			}
			if mode == "error_response" && !strings.Contains(err.Error(), "ACQ.TRADE_NOT_EXIST") {
				t.Fatal("gateway error lost")
			}
		})
	}
}

func TestAlipayQueryByMerchantOrder(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprint(mismatch), func(t *testing.T) {
			a := NewAlipay()
			a.client = &http.Client{Transport: alipayTransport(func(r *http.Request) (*http.Response, error) {
				_ = r.ParseForm()
				verifyAlipayRequest(t, r.PostForm)
				var biz map[string]string
				_ = json.Unmarshal([]byte(r.PostForm.Get("biz_content")), &biz)
				if len(biz) != 1 || biz["out_trade_no"] != "ZP-attempt" {
					t.Fatalf("wrong merchant reference: %+v", biz)
				}
				ref := "ZP-attempt"
				if mismatch {
					ref = "ZP-other"
				}
				raw := `{"code":"10000","trade_status":"TRADE_SUCCESS","trade_no":"T123","out_trade_no":"` + ref + `","total_amount":"10.00"}`
				priv, _ := parseRSAPrivateKey(alipayTestPriv)
				sig, _ := rsa2Sign(priv, []byte(raw))
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"alipay_trade_query_response":` + raw + `,"sign":"` + sig + `"}`)), Header: make(http.Header)}, nil
			})}
			fact, err := a.QueryPaymentByOrderNo(context.Background(), "ZP-attempt", alipayConfigForTest(t))
			if (err != nil) != mismatch {
				t.Fatalf("mismatch=%v fact=%+v err=%v", mismatch, fact, err)
			}
		})
	}
}

func TestAlipayConfigValidation(t *testing.T) {
	for _, gateway := range []string{"", "https://openapi.alipay.com/gateway.do", " https://openapi-sandbox.dl.alipaydev.com/gateway.do "} {
		var cfg map[string]string
		_ = json.Unmarshal(alipayConfigForTest(t), &cfg)
		cfg["gateway"] = gateway
		raw, _ := json.Marshal(cfg)
		if err := NewAlipay().ValidateConfig(raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, gateway := range []string{"not-a-url", "javascript:alert(1)", "https://user:password@example.com/gateway.do", "https://example.com/gateway.do?x=1", "https://example.com/#gateway"} {
		var cfg map[string]string
		_ = json.Unmarshal(alipayConfigForTest(t), &cfg)
		cfg["gateway"] = gateway
		raw, _ := json.Marshal(cfg)
		if err := NewAlipay().ValidateConfig(raw); err == nil {
			t.Fatal("invalid gateway accepted")
		}
	}
}
