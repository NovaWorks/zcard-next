package adapter

// epusdt golden vector 契约测试（P2-09 T1）：
// 签名向量由独立工具（Python hmac）预计算——与实现零耦合（dujiao 同纪律）。
// 覆盖：签名确定性/剔 signature+空值/伪签名拒绝/金额两位小数格式化/
// httptest mock 网关下单全流程/回调验签（含 JSON 数字字面保持）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

const (
	goldenSecret1 = "testsecret"
	goldenSig1    = "0ea1af393183f70d18a2dc8a9a2609b9c58be7eed94b0cb4b98bcd21bdc0036c"
	goldenSecret2 = "sk"
	goldenSig2    = "a627058af0227d96b417e1c0ab5075d00aa96cc49ad8a836b03c934d1b9d7136"
)

// TestEpusdtSignGolden 签名确定性 + 拼接口径（剔 signature/空值、ASCII 序、k=v&）。
func TestEpusdtSignGolden(t *testing.T) {
	p1 := map[string]string{
		"pid": "1001", "order_id": "S215161659327512576", "currency": "cny",
		"amount":       "10.00",
		"notify_url":   "https://shop.example.com/api/v1/payments/callback/epusdt",
		"redirect_url": "https://shop.example.com/payment/S215161659327512576",
		"name":         "S215161659327512576", "network": "TRC20", "token": "USDT",
	}
	if got := epusdtSign(p1, goldenSecret1); got != goldenSig1 {
		t.Fatalf("签名向量 1 不符:\n got  %s\n want %s", got, goldenSig1)
	}
	// signature 与空值参与也不影响结果
	p2 := map[string]string{"amount": "100", "order_id": "ORD-2", "pid": "9", "signature": "deadbeef", "token": ""}
	if got := epusdtSign(p2, goldenSecret2); got != goldenSig2 {
		t.Fatalf("签名向量 2 不符:\n got  %s\n want %s", got, goldenSig2)
	}
}

// TestEpusdtVerifySignRejected 伪签名/换密钥拒绝。
func TestEpusdtVerifySignRejected(t *testing.T) {
	p := map[string]string{"amount": "1.00", "order_id": "A", "pid": "1"}
	sig := epusdtSign(p, "right-key")
	if !epusdtVerifySign(p, "right-key", sig) {
		t.Fatal("正确签名应通过")
	}
	if epusdtVerifySign(p, "wrong-key", sig) {
		t.Fatal("换密钥应拒绝")
	}
	if epusdtVerifySign(p, "right-key", strings.ToUpper(sig)) {
		t.Fatal("伪签名（大小写错位）应拒绝——协议为小写 hex")
	}
}

// TestEpusdtVerifyCallback 回调验签矩阵。
func TestEpusdtVerifyCallback(t *testing.T) {
	cfg := json.RawMessage(`{"api_url":"https://gw","pid":"1001","secret_key":"testsecret"}`)
	cb := func(mutate func(m map[string]string)) map[string]string {
		m := map[string]string{
			"order_id": "S215161659327512576", "trade_id": "T888",
			"amount": "10.00", "status": "2", "actual_amount": "1.4286",
			"token": "USDT", "network": "TRC20",
		}
		if mutate != nil {
			mutate(m)
		}
		m["signature"] = epusdtSign(m, "testsecret")
		return m
	}
	a := NewEpusdt()

	// 正常成功回调
	f, err := a.VerifyCallback(cb(nil), cfg)
	if err != nil || !f.Success {
		t.Fatalf("正常回调失败: %v %+v", err, f)
	}
	if f.OrderNo != "S215161659327512576" || f.ChannelOrderNo != "T888" {
		t.Fatalf("单号错位: %+v", f)
	}
	if f.Amount != money.Cents(1000) || f.Currency != "CNY" {
		t.Fatalf("金额/币种错误: %d %s", f.Amount, f.Currency)
	}

	// 签名后篡改金额 → 验签拒绝（签名覆盖全字段，无密钥不可改）
	tampered := cb(nil)
	tampered["amount"] = "99.00" // 合法签名保留，只改金额
	if _, err := a.VerifyCallback(tampered, cfg); err == nil {
		t.Fatal("签名后篡改必须验签失败")
	}

	// 非 2 状态 → Success=false（验签仍过，管线忽略）
	f2, err := a.VerifyCallback(cb(func(m map[string]string) { m["status"] = "1" }), cfg)
	if err != nil || f2.Success {
		t.Fatalf("status!=2 应 Success=false: %v", err)
	}

	// 缺签名拒绝（cb 会重签——手动去掉）
	noSig := cb(nil)
	delete(noSig, "signature")
	if _, err := a.VerifyCallback(noSig, cfg); err == nil {
		t.Fatal("缺 signature 应拒绝")
	}
}

// TestEpusdtCreatePaymentMock httptest 假网关下单全流程。
func TestEpusdtCreatePaymentMock(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/payments/gmpay/v1/order/create-transaction" {
			w.WriteHeader(404)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"status_code":200,"message":"ok","data":{"trade_id":"T123","payment_url":"https://gw/pay/T123"}}`))
	}))
	defer srv.Close()

	cfg := json.RawMessage(fmt.Sprintf(`{"api_url":%q,"pid":"1001","secret_key":"testsecret"}`, srv.URL))
	a := NewEpusdt()
	info, err := a.CreatePayment(context.Background(), port.CreatePaymentRequest{
		OrderNo: "S215161659327512576", Amount: money.Cents(1000), Subject: "S215161659327512576",
		NotifyBaseURL: "https://shop.example.com/api/v1/payments/callback/epusdt",
		ReturnURL:     "https://shop.example.com/payment/S215161659327512576",
		Config:        cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Type != "redirect" {
		t.Fatalf("应为 redirect: %s", info.Type)
	}
	var payload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(info.Payload, &payload); err != nil || payload.URL != "https://gw/pay/T123" {
		t.Fatalf("支付链接错位: %s %v", payload.URL, err)
	}
	// 请求侧断言：金额两位小数字符串 + 签名可被同算法验证
	if gotBody["amount"] != "10.00" {
		t.Fatalf("金额格式必须两位小数字符串: %q", gotBody["amount"])
	}
	if !epusdtVerifySign(gotBody, "testsecret", gotBody["signature"]) {
		t.Fatal("下单请求签名自验失败")
	}
	// 网关拒绝路径
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status_code":400,"message":"amount too small"}`))
	}))
	defer srv2.Close()
	cfg2 := json.RawMessage(fmt.Sprintf(`{"api_url":%q,"pid":"1","secret_key":"s"}`, srv2.URL))
	if _, err := a.CreatePayment(context.Background(), port.CreatePaymentRequest{OrderNo: "X", Config: cfg2, Amount: 1}); err == nil {
		t.Fatal("网关拒绝应报错")
	}
}

// TestEpusdtAckerAndValidate Acker 应答体 + 凭据校验。
func TestEpusdtAckerAndValidate(t *testing.T) {
	a := NewEpusdt()
	if a.SuccessAck() != "ok" {
		t.Fatalf("ack 应为纯文本 ok: %q", a.SuccessAck())
	}
	if err := a.ValidateConfig(json.RawMessage(`{"api_url":"https://g","pid":"1","secret_key":"s"}`)); err != nil {
		t.Fatal(err)
	}
	if err := a.ValidateConfig(json.RawMessage(`{"pid":"1"}`)); err == nil {
		t.Fatal("缺凭据应拒绝")
	}
}
