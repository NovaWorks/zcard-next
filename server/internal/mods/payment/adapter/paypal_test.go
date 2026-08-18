package adapter

// paypal 契约测试（P2-09 T4）：
// golden vector（token 序列/金额两位小数/capture 状态机/验签体字节拼接）+
// httptest 假 PayPal API 全流程（下单→验签→先查后捕→退款）。

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

const (
	ppClientID     = "golden-client-id"
	ppClientSecret = "golden-client-secret"
)

// ppCfg 渠道凭据（base_url 注入 httptest 假 API）。
func ppCfg(base string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"client_id":%q,"client_secret":%q,"mode":"sandbox","base_url":%q,"webhook_id":"wh_golden","brand_name":"ZCard","target_currency":"USD"}`,
		ppClientID, ppClientSecret, base))
}

// ppServer 假 PayPal API。
func ppServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// ppTokenHandler token 端点（Basic 认证 + grant_type 表单断言；hits 计数）。
func ppTokenHandler(t *testing.T, token, expiresIn string, hits *atomic.Int64) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/oauth2/token" || r.Method != http.MethodPost {
			w.WriteHeader(404)
			return
		}
		if hits != nil {
			hits.Add(1)
		}
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(ppClientID+":"+ppClientSecret))
		if r.Header.Get("Authorization") != wantAuth {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			w.WriteHeader(400)
			return
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "client_credentials" {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(fmt.Sprintf(`{"access_token":%q,"expires_in":%s,"app_id":"APP-1","token_type":"Bearer"}`, token, expiresIn)))
	}
}

// ppOrder 订单资源（reference_id+invoice_id 双写；捕获 COMPLETED $1.40）。
func ppOrder(id, status string) string {
	return fmt.Sprintf(`{"id":%q,"status":%q,"purchase_units":[{"reference_id":"S215161659327512576","invoice_id":"S215161659327512576","amount":{"currency_code":"USD","value":"1.40"},"payments":{"captures":[{"id":"5JX11038EG486731P","status":"COMPLETED","amount":{"currency_code":"USD","value":"1.40"}}]}}]}`, id, status)
}

// ppOrderApproved 已授权未捕获订单（无 captures）。
func ppOrderApproved(id string) string {
	return fmt.Sprintf(`{"id":%q,"status":"APPROVED","purchase_units":[{"reference_id":"S215161659327512576","invoice_id":"S215161659327512576","amount":{"currency_code":"USD","value":"1.40"}}]}`, id)
}

// ppCaptureResp 捕获响应（订单形态；captures[0].status 可控——状态机测试用）。
func ppCaptureResp(captureStatus string) string {
	return fmt.Sprintf(`{"id":%q,"status":"COMPLETED","purchase_units":[{"reference_id":"S215161659327512576","invoice_id":"S215161659327512576","amount":{"currency_code":"USD","value":"1.40"},"payments":{"captures":[{"id":%q,"status":%q,"amount":{"currency_code":"USD","value":"1.40"}}]}}]}`,
		ppOrderID, ppCaptureID, captureStatus)
}

const (
	ppOrderID    = "4KH90297NW664243H"
	ppCaptureID  = "5JX11038EG486731P"
	ppBusinessNo = "S215161659327512576"
)

// ── Provider：下单 ──

// TestPaypalCreatePaymentMock 假 API 下单断言（金额格式化/invoice 幂等/return 端点/品牌）。
func TestPaypalCreatePaymentMock(t *testing.T) {
	var gotBody []byte
	srv := ppServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/oauth2/token" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"access_token":"tok-1","expires_in":3600}`))
		case r.URL.Path == "/v2/checkout/orders" && r.Method == http.MethodPost:
			gotBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"id":"` + ppOrderID + `","status":"CREATED","links":[{"href":"https://www.sandbox.paypal.com/checkoutnow?token=` + ppOrderID + `","rel":"approve","method":"GET"}]}`))
		default:
			w.WriteHeader(404)
		}
	})
	a := NewPaypal()
	info, err := a.CreatePayment(context.Background(), port.CreatePaymentRequest{
		OrderNo: ppBusinessNo, Amount: money.Cents(1000), Subject: "订单 " + ppBusinessNo,
		Channel: "paypal", Config: ppCfg(srv.URL), ChargedUnits: 140, ChargedCurrency: "USD",
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(info.Payload, &payload)
	if payload.URL != "https://www.sandbox.paypal.com/checkoutnow?token="+ppOrderID {
		t.Fatalf("授权链接错位: %s", payload.URL)
	}
	// 请求体断言：intent/幂等双写/金额两位小数/跨币/return 同步捕获端点/品牌
	for _, want := range []string{
		`"intent":"CAPTURE"`,
		`"reference_id":"` + ppBusinessNo + `"`,
		`"invoice_id":"` + ppBusinessNo + `"`,
		`"value":"1.40"`, // 金额两位小数字符串（任务书 §3.1 钉死）
		`"currency_code":"USD"`,
		`"return_url":"/payments/return/paypal"`,
		`"user_action":"PAY_NOW"`,
		`"shipping_preference":"NO_SHIPPING"`,
		`"brand_name":"ZCard"`,
	} {
		if !bytes.Contains(gotBody, []byte(want)) {
			t.Fatalf("下单请求缺 %q\nbody: %s", want, gotBody)
		}
	}
	// 快照缺席 + 渠道声明跨币目标 → fail-closed 拒绝
	if _, err := a.CreatePayment(context.Background(), port.CreatePaymentRequest{
		OrderNo: "X", Amount: 1000, Config: ppCfg(srv.URL), // ChargedUnits=0
	}); err == nil {
		t.Fatal("跨币渠道快照缺失应拒绝")
	}
}

// TestPaypalCreateFallbackURL 无 approve 链接时按 1.x 兜底拼授权页。
func TestPaypalCreateFallbackURL(t *testing.T) {
	srv := ppServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/oauth2/token":
			_, _ = w.Write([]byte(`{"access_token":"tok-1","expires_in":3600}`))
		case r.URL.Path == "/v2/checkout/orders":
			_, _ = w.Write([]byte(`{"id":"` + ppOrderID + `","status":"CREATED"}`)) // 无 links
		default:
			w.WriteHeader(404)
		}
	})
	info, err := NewPaypal().CreatePayment(context.Background(), port.CreatePaymentRequest{
		OrderNo: ppBusinessNo, Amount: 1000, Channel: "paypal", Config: ppCfg(srv.URL),
		ChargedUnits: 140, ChargedCurrency: "USD",
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(info.Payload, &payload)
	want := "https://www.sandbox.paypal.com/checkoutnow?token=" + ppOrderID
	if payload.URL != want {
		t.Fatalf("兜底授权链接错位: got %s want %s", payload.URL, want)
	}
}

// ── token 缓存（提前 5 分钟失效；expires_in<=300 不缓存）──

// TestPaypalTokenCache 长寿 token 二次调用复用（只换发一次）。
func TestPaypalTokenCache(t *testing.T) {
	var tokenHits atomic.Int64
	srv := ppServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/oauth2/token":
			ppTokenHandler(t, "tok-cached", "3600", &tokenHits)(w, r)
		case r.URL.Path == "/v2/checkout/orders":
			_, _ = w.Write([]byte(`{"id":"` + ppOrderID + `","status":"CREATED","links":[{"href":"https://x/approve","rel":"approve"}]}`))
		default:
			w.WriteHeader(404)
		}
	})
	a := NewPaypal()
	req := port.CreatePaymentRequest{OrderNo: ppBusinessNo, Amount: 1000, Channel: "paypal",
		Config: ppCfg(srv.URL), ChargedUnits: 140, ChargedCurrency: "USD"}
	for i := 0; i < 2; i++ {
		if _, err := a.CreatePayment(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if got := tokenHits.Load(); got != 1 {
		t.Fatalf("token 应缓存复用（换发 %d 次，期望 1）", got)
	}
}

// TestPaypalTokenNoCacheShortLived expires_in<=300 的短寿 token 不缓存（每次换发）。
func TestPaypalTokenNoCacheShortLived(t *testing.T) {
	var tokenHits atomic.Int64
	srv := ppServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/oauth2/token":
			ppTokenHandler(t, "tok-short", "100", &tokenHits)(w, r)
		case r.URL.Path == "/v2/checkout/orders":
			_, _ = w.Write([]byte(`{"id":"` + ppOrderID + `","status":"CREATED","links":[{"href":"https://x/approve","rel":"approve"}]}`))
		default:
			w.WriteHeader(404)
		}
	})
	a := NewPaypal()
	req := port.CreatePaymentRequest{OrderNo: ppBusinessNo, Amount: 1000, Channel: "paypal",
		Config: ppCfg(srv.URL), ChargedUnits: 140, ChargedCurrency: "USD"}
	for i := 0; i < 2; i++ {
		if _, err := a.CreatePayment(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if got := tokenHits.Load(); got != 2 {
		t.Fatalf("短寿 token 不应缓存（换发 %d 次，期望 2）", got)
	}
}

// ── Capturer：先查后捕状态机 ──

// TestPaypalQueryCaptureStateMachine 订单状态机：APPROVED→捕获；COMPLETED→幂等短路；
// CREATED→未付；捕获 PENDING→未成功；单号非法→零出站。
func TestPaypalQueryCaptureStateMachine(t *testing.T) {
	var captureCalls atomic.Int64
	orderStatus := "APPROVED"
	captureStatus := "COMPLETED"
	srv := ppServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/oauth2/token":
			_, _ = w.Write([]byte(`{"access_token":"tok-1","expires_in":3600}`))
		case r.URL.Path == "/v2/checkout/orders/"+ppOrderID && r.Method == http.MethodGet:
			if orderStatus == "APPROVED" {
				_, _ = w.Write([]byte(ppOrderApproved(ppOrderID)))
			} else {
				_, _ = w.Write([]byte(ppOrder(ppOrderID, orderStatus)))
			}
		case r.URL.Path == "/v2/checkout/orders/"+ppOrderID+"/capture" && r.Method == http.MethodPost:
			captureCalls.Add(1)
			_, _ = w.Write([]byte(ppCaptureResp(captureStatus)))
		default:
			w.WriteHeader(404)
		}
	})
	a := NewPaypal()
	cfg := ppCfg(srv.URL)

	// 1) APPROVED → 先查后捕 → 成功
	f, err := a.QueryPayment(context.Background(), ppOrderID, cfg)
	if err != nil || !f.Success {
		t.Fatalf("APPROVED 捕获失败: %v %+v", err, f)
	}
	if captureCalls.Load() != 1 {
		t.Fatalf("应触发捕获 1 次，实际 %d", captureCalls.Load())
	}
	if f.OrderNo != ppBusinessNo || f.ChannelOrderNo != ppOrderID || f.Amount != 140 || f.Currency != "USD" {
		t.Fatalf("捕获事实错位: %+v", f)
	}

	// 2) 重复捕获（先查后捕）：COMPLETED → 幂等短路（零捕获调用）且成功
	orderStatus = "COMPLETED"
	f, err = a.QueryPayment(context.Background(), ppOrderID, cfg)
	if err != nil || !f.Success {
		t.Fatalf("COMPLETED 幂等短路失败: %v %+v", err, f)
	}
	if captureCalls.Load() != 1 {
		t.Fatalf("COMPLETED 不应再捕获（先查后捕），实际 %d", captureCalls.Load())
	}

	// 3) CREATED → 未支付（Success=false，OrderNo 仍携带）
	orderStatus = "CREATED"
	f, err = a.QueryPayment(context.Background(), ppOrderID, cfg)
	if err != nil || f.Success {
		t.Fatalf("CREATED 应为未支付: %v %+v", err, f)
	}
	if f.OrderNo != ppBusinessNo {
		t.Fatalf("未支付事实仍应携带 OrderNo: %+v", f)
	}

	// 4) 捕获响应 PENDING（eCheck）→ 未成功（等后续 COMPLETED webhook）
	orderStatus = "APPROVED"
	captureStatus = "PENDING"
	f, err = a.QueryPayment(context.Background(), ppOrderID, cfg)
	if err != nil || f.Success {
		t.Fatalf("捕获 PENDING 不应判成功: %v %+v", err, f)
	}

	// 5) 单号格式非法 → 拒绝（匿名端点出站放大器防护）
	if _, err := a.QueryPayment(context.Background(), "tiny", cfg); err == nil {
		t.Fatal("非法单号应拒绝")
	}
	if _, err := a.QueryPayment(context.Background(), "a_bad_order_id!", cfg); err == nil {
		t.Fatal("非法单号应拒绝")
	}
}

// ── Webhook：官方验签 API + 事件映射 ──

// ppWebhookHeaders 五头（PayPal 验签 API 输入）。
func ppWebhookHeaders() map[string]string {
	return map[string]string{
		"Paypal-Transmission-Id":   "trans-1",
		"Paypal-Transmission-Time": "2026-08-18T00:00:00Z",
		"Paypal-Cert-Url":          "https://api-m.paypal.com/v1/notifications/certs/CERT-1",
		"Paypal-Auth-Algo":         "SHA256withRSA",
		"Paypal-Transmission-Sig":  "sig-golden",
	}
}

func ppWebhookCaptureCompleted() []byte {
	return []byte(`{"id":"WH-1","event_type":"PAYMENT.CAPTURE.COMPLETED","resource_type":"capture","resource":{"id":"` + ppCaptureID + `","status":"COMPLETED","amount":{"currency_code":"USD","value":"1.40"},"supplementary_data":{"related_ids":{"order_id":"` + ppOrderID + `"}}}}`)
}

func ppWebhookOrderCompleted() []byte {
	return []byte(`{"id":"WH-2","event_type":"CHECKOUT.ORDER.COMPLETED","resource_type":"checkout-order","resource":` + ppOrder(ppOrderID, "COMPLETED") + `}`)
}

func ppWebhookOrderCompletedInvoiceOnly() []byte {
	return []byte(`{"id":"WH-2b","event_type":"CHECKOUT.ORDER.COMPLETED","resource_type":"checkout-order","resource":{"id":"` + ppOrderID + `","status":"COMPLETED","purchase_units":[{"invoice_id":"` + ppBusinessNo + `","amount":{"currency_code":"USD","value":"1.40"},"payments":{"captures":[{"id":"` + ppCaptureID + `","status":"COMPLETED","amount":{"currency_code":"USD","value":"1.40"}}]}}]}}`)
}

func ppWebhookEvent(eventType string) []byte {
	return []byte(`{"id":"WH-3","event_type":"` + eventType + `","resource":{"id":"` + ppCaptureID + `","status":"COMPLETED"}}`)
}

// TestPaypalWebhookGolden 验签 API + 事件映射矩阵：
// 验签请求体 webhook_event 必须与原始事件字节完全一致（签名覆盖原文）；
// capture 事件经 related_ids 出站拉订单解析 invoice；order 事件零出站。
func TestPaypalWebhookGolden(t *testing.T) {
	var verifyBody []byte
	var orderGets atomic.Int64
	srv := ppServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/oauth2/token":
			_, _ = w.Write([]byte(`{"access_token":"tok-1","expires_in":3600}`))
		case r.URL.Path == "/v1/notifications/verify-webhook-signature":
			verifyBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"verification_status":"SUCCESS"}`))
		case strings.HasPrefix(r.URL.Path, "/v2/checkout/orders/") && r.Method == http.MethodGet:
			orderGets.Add(1)
			_, _ = w.Write([]byte(ppOrder(ppOrderID, "COMPLETED")))
		default:
			w.WriteHeader(404)
		}
	})
	a := NewPaypal()
	cfg := ppCfg(srv.URL)

	// 1) CHECKOUT.ORDER.COMPLETED（资源自带 invoice——零出站拉单）
	body := ppWebhookOrderCompleted()
	f, err := a.ParseWebhook(ppWebhookHeaders(), body, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Success || f.OrderNo != ppBusinessNo || f.ChannelOrderNo != ppOrderID || f.Amount != 140 || f.Currency != "USD" {
		t.Fatalf("order 事件事实错位: %+v", f)
	}
	if orderGets.Load() != 0 {
		t.Fatalf("order 事件不应出站拉单（资源自带 invoice）")
	}
	// 验签请求体：webhook_event 字节 === 原始事件（签名覆盖原文）
	var vr struct {
		WebhookEvent json.RawMessage `json:"webhook_event"`
		WebhookID    string          `json:"webhook_id"`
	}
	if err := json.Unmarshal(verifyBody, &vr); err != nil {
		t.Fatalf("验签请求体解析失败: %v\n%s", err, verifyBody)
	}
	if !bytes.Equal(vr.WebhookEvent, body) {
		t.Fatalf("验签体 webhook_event 必须原样透传:\ngot  %s\nwant %s", vr.WebhookEvent, body)
	}
	if vr.WebhookID != "wh_golden" {
		t.Fatalf("验签体 webhook_id 错位: %s", vr.WebhookID)
	}

	// 2) PAYMENT.CAPTURE.COMPLETED（capture 资源无 invoice——出站拉订单解析）
	body = ppWebhookCaptureCompleted()
	f, err = a.ParseWebhook(ppWebhookHeaders(), body, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Success || f.OrderNo != ppBusinessNo || f.ChannelOrderNo != ppOrderID || f.Amount != 140 {
		t.Fatalf("capture 事件事实错位: %+v", f)
	}
	if orderGets.Load() != 1 {
		t.Fatalf("capture 事件应出站拉单解析 invoice，实际 %d", orderGets.Load())
	}

	// 3) 仅 invoice_id（reference_id 缺席）→ 兜底取 invoice_id
	f, err = a.ParseWebhook(ppWebhookHeaders(), ppWebhookOrderCompletedInvoiceOnly(), cfg)
	if err != nil || !f.Success || f.OrderNo != ppBusinessNo {
		t.Fatalf("invoice_id 兜底失败: %v %+v", err, f)
	}

	// 4) 验签 API 返回 FAILURE → 拒
	verifyBody = nil
	failSrv := ppServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/oauth2/token":
			_, _ = w.Write([]byte(`{"access_token":"tok-1","expires_in":3600}`))
		case r.URL.Path == "/v1/notifications/verify-webhook-signature":
			_, _ = w.Write([]byte(`{"verification_status":"FAILURE"}`))
		default:
			w.WriteHeader(404)
		}
	})
	if _, err := a.ParseWebhook(ppWebhookHeaders(), ppWebhookOrderCompleted(), ppCfg(failSrv.URL)); err == nil {
		t.Fatal("验签 FAILURE 应拒")
	}

	// 5) 缺头 → 拒（验签 API 不可达）
	if _, err := a.ParseWebhook(map[string]string{"Paypal-Transmission-Id": "x"}, ppWebhookOrderCompleted(), cfg); err == nil {
		t.Fatal("缺头应拒")
	}

	// 6) 非成功事件（pending/denied）→ Success=false（管线忽略路径）
	for _, et := range []string{"PAYMENT.CAPTURE.PENDING", "PAYMENT.CAPTURE.DENIED", "CHECKOUT.ORDER.DENIED", "CHECKOUT.ORDER.APPROVED"} {
		f, err := a.ParseWebhook(ppWebhookHeaders(), ppWebhookEvent(et), cfg)
		if err != nil || f.Success {
			t.Fatalf("%s 事件应 Success=false: %v %+v", et, err, f)
		}
	}
}

// ── Refunder：captures/{id}/refund ──

// TestPaypalRefundMock 退款：order 锚点解析 capture id；部分退金额两位小数；全额退空体。
func TestPaypalRefundMock(t *testing.T) {
	var refundPath, refundBody string
	srv := ppServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/oauth2/token":
			_, _ = w.Write([]byte(`{"access_token":"tok-1","expires_in":3600}`))
		case r.URL.Path == "/v2/checkout/orders/"+ppOrderID && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(ppOrder(ppOrderID, "COMPLETED")))
		case strings.HasPrefix(r.URL.Path, "/v2/payments/captures/") && r.Method == http.MethodPost:
			refundPath = r.URL.Path
			b, _ := io.ReadAll(r.Body)
			refundBody = string(b)
			_, _ = w.Write([]byte(`{"id":"REF-1","status":"COMPLETED"}`))
		default:
			w.WriteHeader(404)
		}
	})
	a := NewPaypal()
	cfg := ppCfg(srv.URL)

	// 部分退（140 单位 = $1.40，渠道币种口径）
	if err := a.Refund(context.Background(), ppOrderID, 140, "测试退款", cfg); err != nil {
		t.Fatal(err)
	}
	if refundPath != "/v2/payments/captures/"+ppCaptureID+"/refund" {
		t.Fatalf("退款锚点错位: %s", refundPath)
	}
	if !strings.Contains(refundBody, `"value":"1.40"`) || !strings.Contains(refundBody, `"currency_code":"USD"`) {
		t.Fatalf("部分退金额错位: %s", refundBody)
	}

	// 全额退（amount<=0 → 空对象）
	if err := a.Refund(context.Background(), ppOrderID, 0, "", cfg); err != nil {
		t.Fatal(err)
	}
	if refundBody != "{}" {
		t.Fatalf("全额退应为空体: %s", refundBody)
	}

	// 无可退捕获 → 拒
	emptySrv := ppServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/oauth2/token":
			_, _ = w.Write([]byte(`{"access_token":"tok-1","expires_in":3600}`))
		case r.URL.Path == "/v2/checkout/orders/"+ppOrderID:
			_, _ = w.Write([]byte(fmt.Sprintf(`{"id":%q,"status":"COMPLETED","purchase_units":[{"reference_id":"X","amount":{"currency_code":"USD","value":"1.40"}}]}`, ppOrderID)))
		default:
			w.WriteHeader(404)
		}
	})
	if err := a.Refund(context.Background(), ppOrderID, 140, "", ppCfg(emptySrv.URL)); err == nil {
		t.Fatal("无可退捕获应拒")
	}
}

// ── 凭据校验 ──

// TestPaypalValidateConfig 凭据校验。
func TestPaypalValidateConfig(t *testing.T) {
	a := NewPaypal()
	if err := a.ValidateConfig(ppCfg("https://x")); err != nil {
		t.Fatal(err)
	}
	if err := a.ValidateConfig(json.RawMessage(`{"client_id":"x"}`)); err == nil {
		t.Fatal("缺 client_secret 应拒绝")
	}
	if err := a.ValidateConfig(json.RawMessage(`{"client_id":"x","client_secret":"y","mode":"staging"}`)); err == nil {
		t.Fatal("非法 mode 应拒绝")
	}
	// base_url 覆盖 + 空 mode → 合法
	if err := a.ValidateConfig(json.RawMessage(`{"client_id":"x","client_secret":"y","base_url":"https://api-m.sandbox.paypal.com"}`)); err != nil {
		t.Fatal(err)
	}
}
