package adapter

// stripe 契约测试（）：
// webhook golden vector（独立 Python hmac 预计算——与 SDK/实现零耦合）+
// httptest 假 Stripe API（backend 注入）下单/查单/退款断言。

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/stripe/stripe-go/v82"
)

// 签名按 Stripe 文档算法在测试内独立构造（crypto/hmac 直算，不经 SDK 验签代码）：
// v1 = HMAC-SHA256(whsec, "t=<ts>.<payload>")；header "t=<ts>,v1=<sig>"
// （SDK 默认 tolerance 300s——时间戳须取当前，固定向量会超时窗）
const (
	goldenWhsec  = "whsec_golden_test"
	stripeAPIKey = "sk_test_golden"
)

func goldenWebhookPayload() []byte {
	b, _ := io.ReadAll(strings.NewReader(`{"id":"evt_1","object":"event","type":"checkout.session.completed","data":{"object":{"id":"cs_test_1","object":"checkout_session","client_reference_id":"S215161659327512576","metadata":{"order_no":"S215161659327512576"},"amount_total":140,"currency":"usd","payment_intent":"pi_3XX"}}}`))
	return b
}

// signStripe 独立构造签名（Stripe 文档算法 crypto/hmac 直算，不经 SDK 验签代码）：
// v1 = HMAC-SHA256(whsec, "t=<ts>.<payload>")；header "t=<ts>,v1=<sig>"
func signStripe(t *testing.T, payload []byte, secret string) string {
	t.Helper()
	ts := fmt.Sprintf("%d", time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + string(payload)))
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func goldenCfg() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"secret_key":%q,"webhook_secret":%q,"target_currency":"USD"}`, stripeAPIKey, goldenWhsec))
}

// withStripeBackend 注入 httptest 假 API（测试后恢复）。
func withStripeBackend(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(func() { srv.Close(); stripeBackendOverride = stripe.Backends{} })
	bc := &stripe.BackendConfig{URL: stripe.String(srv.URL), EnableTelemetry: stripe.Bool(false)}
	stripeBackendOverride = stripe.Backends{API: stripe.GetBackendWithConfig(stripe.APIBackend, bc)}
	return srv
}

// TestStripeWebhookGolden 签名验签矩阵（向量独立预计算）。
func TestStripeWebhookGolden(t *testing.T) {
	a := NewStripe()
	body := goldenWebhookPayload()
	hdr := map[string]string{"Stripe-Signature": signStripe(t, body, goldenWhsec)}

	// 1) 正确签名 → 成功事件解析（order_no/pi/金额/币种）
	f, err := a.ParseWebhook(hdr, body, goldenCfg())
	if err != nil {
		t.Fatal(err)
	}
	if !f.Success || f.OrderNo != "S215161659327512576" {
		t.Fatalf("事件解析错位: %+v", f)
	}
	if f.ChannelOrderNo != "pi_3XX" || f.Amount != 140 || f.Currency != "USD" {
		t.Fatalf("金额/锚点错位: %+v", f)
	}

	// 2) 篡改 payload → 拒（签名对不上）
	if _, err := a.ParseWebhook(hdr, append(body, 'x'), goldenCfg()); err == nil {
		t.Fatal("篡改 payload 应拒")
	}
	// 3) 伪签名 → 拒（当前 ts + 死值）
	badHdr := map[string]string{"Stripe-Signature": fmt.Sprintf("t=%d,v1=deadbeef", time.Now().Unix())}
	if _, err := a.ParseWebhook(badHdr, body, goldenCfg()); err == nil {
		t.Fatal("伪签名应拒")
	}
	// 4) 换密钥 → 拒
	otherCfg := json.RawMessage(fmt.Sprintf(`{"secret_key":%q,"webhook_secret":"whsec_other"}`, stripeAPIKey))
	if _, err := a.ParseWebhook(hdr, body, otherCfg); err == nil {
		t.Fatal("换密钥应拒")
	}
	// 5) 缺头 → 拒
	if _, err := a.ParseWebhook(map[string]string{}, body, goldenCfg()); err == nil {
		t.Fatal("缺 Stripe-Signature 应拒")
	}
	// 6) 非成功事件（expired 重签）→ Success=false（管线忽略路径）
	expired := []byte(strings.Replace(string(body), "checkout.session.completed", "checkout.session.expired", 1))
	f2, err := a.ParseWebhook(map[string]string{"Stripe-Signature": signStripe(t, expired, goldenWhsec)}, expired, goldenCfg())
	if err != nil || f2.Success {
		t.Fatal("expired 事件应 Success=false")
	}
}

// TestStripeCreatePaymentMock 假 API 下单断言（跨币快照口径）。
func TestStripeCreatePaymentMock(t *testing.T) {
	var gotBody string
	withStripeBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/checkout/sessions" {
			w.WriteHeader(404)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"id":"cs_1","object":"checkout_session","url":"https://checkout.stripe.com/c/pay/cs_1"}`))
	})
	a := NewStripe()
	info, err := a.CreatePayment(context.Background(), port.CreatePaymentRequest{
		OrderNo: "S215161659327512576", Amount: money.Cents(1000), Subject: "订单 S215161659327512576",
		Config: goldenCfg(), ChargedUnits: 140, ChargedCurrency: "USD",
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(info.Payload, &payload)
	if payload.URL != "https://checkout.stripe.com/c/pay/cs_1" {
		t.Fatalf("收银台链接错位: %s", payload.URL)
	}
	// 表单断言：跨币金额/币种/metadata 双写/mode
	for _, want := range []string{
		"mode=payment",
		"line_items[0][price_data][unit_amount]=140",
		"line_items[0][price_data][currency]=usd",
		"client_reference_id=S215161659327512576",
		"metadata[order_no]=S215161659327512576",
	} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("下单表单缺 %q\nbody: %s", want, gotBody)
		}
	}

	// 快照缺席 + 渠道声明跨币目标 → fail-closed 拒绝
	if _, err := a.CreatePayment(context.Background(), port.CreatePaymentRequest{
		OrderNo: "X", Amount: 1000, Config: goldenCfg(), // ChargedUnits=0
	}); err == nil {
		t.Fatal("跨币渠道快照缺失应拒绝")
	}
}

// TestStripeQueryAndRefundMock 查单/退款 mock。
func TestStripeQueryAndRefundMock(t *testing.T) {
	withStripeBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/checkout/sessions/cs_1":
			_, _ = w.Write([]byte(`{"id":"cs_1","object":"checkout_session","client_reference_id":"S123","metadata":{"order_no":"S123"},"amount_total":140,"currency":"usd","payment_status":"paid","payment_intent":"pi_9"}`))
		case "/v1/refunds":
			_, _ = w.Write([]byte(`{"id":"re_1","object":"refund","status":"succeeded"}`))
		default:
			w.WriteHeader(404)
		}
	})
	a := NewStripe()
	// 查单补单
	f, err := a.QueryPayment(context.Background(), "cs_1", goldenCfg())
	if err != nil || !f.Success {
		t.Fatalf("查单失败: %v %+v", err, f)
	}
	if f.Amount != 140 || f.ChannelOrderNo != "pi_9" || f.OrderNo != "S123" {
		t.Fatalf("查单错位: %+v", f)
	}
	// 退款（pi 锚点 + 渠道币种单位）
	if err := a.Refund(context.Background(), "pi_9", 140, "测试退款", goldenCfg()); err != nil {
		t.Fatal(err)
	}
	// 非 pi_ 锚点拒绝
	if err := a.Refund(context.Background(), "cs_1", 140, "", goldenCfg()); err == nil {
		t.Fatal("cs_ 锚点应拒绝（须 payment_intent）")
	}
}

// TestStripeValidateConfig 凭据校验。
func TestStripeValidateConfig(t *testing.T) {
	a := NewStripe()
	if err := a.ValidateConfig(goldenCfg()); err != nil {
		t.Fatal(err)
	}
	if err := a.ValidateConfig(json.RawMessage(`{"secret_key":"sk"}`)); err == nil {
		t.Fatal("缺 webhook_secret 应拒绝")
	}
}
