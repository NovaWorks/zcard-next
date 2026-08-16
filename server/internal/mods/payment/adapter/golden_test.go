package adapter

// golden vector 契约测试（§5.5.2 门禁化）：
// 固定 key + 固定 body → 期望签名/验签结果，硬编码期望值，
// 防协议口径回归（签名哈希的字节 === 实际发出的字节）。

import (
	"encoding/json"
	"testing"
)

// ── sortParams 签名串构造（字节顺序是资损红线）────────────────

func TestSortParamsGolden(t *testing.T) {
	params := map[string]string{
		"pid":          "1000",
		"out_trade_no": "S20260816001",
		"money":        "12.34",
		"sign_type":    "MD5",
		"sign":         "x",
	}
	got := sortParams(params, "sign", "sign_type")
	want := "money=12.34&out_trade_no=S20260816001&pid=1000"
	if got != want {
		t.Fatalf("签名串 = %q, want %q", got, want)
	}
}

// ── epay MD5 口径 ────────────────────────────────────────────

func TestEpaySignGolden(t *testing.T) {
	params := map[string]string{
		"pid":          "1000",
		"out_trade_no": "S20260816001",
		"money":        "12.34",
		"sign_type":    "MD5",
	}
	got := md5Hex(sortParams(params, "sign", "sign_type") + "testkey123")
	want := "5d1ebf6ae8f15b9c11cb5e3b7a58934f"
	if got != want {
		t.Fatalf("epay sign = %s, want %s", got, want)
	}
}

func TestEpayVerifyCallback(t *testing.T) {
	cfg, _ := json.Marshal(epayConfig{PID: "1000", Key: "testkey123"})
	a := NewEpay()

	form := map[string]string{
		"pid":          "1000",
		"trade_no":     "T20260816001",
		"out_trade_no": "S20260816001",
		"money":        "12.34",
		"trade_status": "TRADE_SUCCESS",
	}
	form["sign"] = md5Hex(sortParams(form, "sign", "sign_type") + "testkey123")

	f, err := a.VerifyCallback(form, cfg)
	if err != nil {
		t.Fatalf("验签失败: %v", err)
	}
	if f.Amount != 1234 {
		t.Fatalf("金额 = %d, want 1234", f.Amount)
	}
	if !f.Success || f.OrderNo != "S20260816001" {
		t.Fatalf("fact = %+v", f)
	}

	// 篡改金额 → 验签失败
	tampered := map[string]string{}
	for k, v := range form {
		tampered[k] = v
	}
	tampered["money"] = "99.99"
	if _, err := a.VerifyCallback(tampered, cfg); err == nil {
		t.Fatal("篡改金额应验签失败")
	}
}

// ── wechat v2 MD5 口径 ───────────────────────────────────────

func TestWechatSignGolden(t *testing.T) {
	c := wechatConfig{AppID: "wx123", MchID: "10001", APIKey: "testapikey"}
	params := map[string]string{
		"appid":        "wx123",
		"mch_id":       "10001",
		"nonce_str":    "abc",
		"out_trade_no": "S20260816001",
		"total_fee":    "1234",
		"trade_type":   "NATIVE",
	}
	got := c.sign(params)
	want := "5A78E8AA821B680C27C353FCB0F96D15" // 大写 hex
	if got != want {
		t.Fatalf("wechat sign = %s, want %s", got, want)
	}
}

func TestWechatVerifyCallback(t *testing.T) {
	cfg, _ := json.Marshal(wechatConfig{AppID: "wx123", MchID: "10001", APIKey: "testapikey"})
	a := NewWechat()
	c := wechatConfig{APIKey: "testapikey"}

	form := map[string]string{
		"return_code":    "SUCCESS",
		"result_code":    "SUCCESS",
		"appid":          "wx123",
		"mch_id":         "10001",
		"out_trade_no":   "S20260816001",
		"total_fee":      "1234",
		"transaction_id": "4200001234",
	}
	form["sign"] = c.sign(form)

	f, err := a.VerifyCallback(form, cfg)
	if err != nil {
		t.Fatalf("验签失败: %v", err)
	}
	if f.Amount != 1234 || !f.Success {
		t.Fatalf("fact = %+v", f)
	}

	tampered := map[string]string{}
	for k, v := range form {
		tampered[k] = v
	}
	tampered["total_fee"] = "9999"
	if _, err := a.VerifyCallback(tampered, cfg); err == nil {
		t.Fatal("篡改金额应验签失败")
	}
}

// ── alipay RSA2 口径 ─────────────────────────────────────────

const alipayTestPriv = `-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC3UEw2dCytducU
shz7THxu4GBkF/7UrH4ds9qd+oaNnQE1TZq/k8erVaomhVlu0AiuhQJpUBBuFFVL
25xFvP89QvmufASxwjzUDAw8AzAC6Irt4S+T+BGwFaXBnzHqolGJBo1lv8BfuBdk
mQJNLqsW+IdDcFtBspTD+SwJL41ea6/d/5TqU/8AmefHWmwVtkuQLyNdJTrCshlh
glxUnV+cAfKRfMbb8TeEU4t5fD159LMogIDe8BiUGs/zTw/ndHoVigSkAdJ7GC3O
ZTyeU8u7iGa6Nivb+O/7rQrfH+2LB6woVKwalyTCm8wS1XKrCEpU5yBZud8As+J0
9WiEKyuxAgMBAAECggEADVAUENpUCk75zjt3NlriKI08AtjpRVm3mQGgTVvN3Mf7
t/rIU8RwOkWw4zZI/e62yrHXMH3Di7MHVMiAq2Hj6XeNOXpBPwWbtEyhsNQMbxGj
UU5KzcS0yCRyUsL0dZVPNZPXvf10g58Td9dS3vcWLsdiz2eAAR/uhOL6KzqcWjB5
ReNNSUcKEPPhlJ9lsZdeG2buwB6eYcltWAFRQFp3s611dvGqA0kmUjYFj/yyDZw5
ZlpsoQb+H7lig8uhOxxWDpGcgKbKDmJ2uOrb3k3Sx9dshQZlWk90LhsBawUCG0/d
5wFpjuoL6SgaO7Jl8Yu7dt313jL6UfbOy/O/05B4iQKBgQDwIbs6naJhWlPMdv8x
CMae/tn0xN0i0pyY/B6SLp40aAqsHGy+vdLgPn2Mk6fr+HB4+uQuGAt21mMp34Kq
EmLVWpCa2jxyKGZ3YN9v+K6p1c5p5n0d1MhnwZHfYnKWAtU5OZRNYtyrAE8hVYIX
rxk7JvRFDToxNHNpR9x5GBr45QKBgQDDbWJUZ9a8xAxBvp8KDRFA5fAUsYqukuhy
oVOxiBe/HCEq80tOt0utgEi8Y7RQF0yA0szrxvC5zJbMf6JH8MrngyJXZ9zX2wsn
p/wkhsmtHdeooEDLFDCIW7aPSan5MvIihxYvGTZGtDvBvo4+qw4stAtKTpPtwQDi
BS9Slro23QKBgQDvGEb6GBakZHHntdxmEFzj1tFh29prX9U4pmAyIWS4vZdSw4Kr
cQpU6SPNIwAh/l7OttEX7C0OCGz4Nmo9uMzbrq8o4H8rE3rjBnuzW6Ndy1sZKrwN
Rd69IImEKNv67ZssvV4ip3sccNRZVnCP8HJo6WJylrcIYzc+7qRhllTU2QKBgF8A
ZsOncwFywI6ZTxEAxzloTiyRHly9N9i5ykjMYtbZotoRSbOrcVOXwEQsp/QjT2J0
l3+qx01bQpeJGGemi8y9t80LxZT9e8+8XtuW1qWck0D7HmRanTk8dGP1qHZnKMRW
LReaRwNaDI6jxtx6JTrgD3kA9/KlV3uIj7ezZDTVAoGAcEfbq/VxgfDr/eohd9ED
0EusAAaGA+3thUVNXjAsQSspfg3NtpX6Yz6avinNsVgLhw2dQG7z9nwTLOqRGY+K
9+xc7KxBuktBC5JESdGJy4rZPZwCV2NwvcKE5+Om2FhgAHYG0gkgd5SKx+YAJK7b
F8swFtpjKpDl8FbWzlV03DA=
-----END PRIVATE KEY-----`

const alipayTestPub = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAt1BMNnQsrXbnFLIc+0x8
buBgZBf+1Kx+HbPanfqGjZ0BNU2av5PHq1WqJoVZbtAIroUCaVAQbhRVS9ucRbz/
PUL5rnwEscI81AwMPAMwAuiK7eEvk/gRsBWlwZ8x6qJRiQaNZb/AX7gXZJkCTS6r
FviHQ3BbQbKUw/ksCS+NXmuv3f+U6lP/AJnnx1psFbZLkC8jXSU6wrIZYYJcVJ1f
nAHykXzG2/E3hFOLeXw9efSzKICA3vAYlBrP808P53R6FYoEpAHSexgtzmU8nlPL
u4hmujYr2/jv+60K3x/tiwesKFSsGpckwpvMEtVyqwhKVOcgWbnfALPidPVohCsr
sQIDAQAB
-----END PUBLIC KEY-----`

const alipayGoldenSign = "YIC7Q6H4ZpFGVvDSqAxQ1yl8hGmTpkOh8S/92+Rl4TH8nxhE+22vV45NX3XU6MzPjiFCP3GWOzM7hvqgfoM+fyPRy6HQZoh03U0H40dmzKliopGdNLZ26NpCfNBmgLYl2NovKW8PdsPo8G66oNtzJuPQTVAfZyhfpcp6U4r9vCuT+dmlSRonfv2d6icrmP3xt5pNJTblq8++RbOgBqf1EWnVoIc1r/L8L58D3IaXEUio6Wq/2CJeFu1XWxHAoB+fTEXRYBL6C6U1DII2DxnIxyK38klAfRQBA04KFELRl1/pC0/B8pClgF0vjYAj86etcFob/V+LpBo10WO1tC7tVw=="

func TestAlipaySignGolden(t *testing.T) {
	priv, err := parseRSAPrivateKey(alipayTestPriv)
	if err != nil {
		t.Fatalf("私钥解析失败: %v", err)
	}
	msg := "app_id=2021001&charset=utf-8&format=JSON&method=alipay.trade.page.pay&timestamp=2026-08-16 12:00:00&version=1.0"
	got, err := rsa2Sign(priv, []byte(msg))
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}
	if got != alipayGoldenSign {
		t.Fatalf("alipay 签名与 golden 不一致")
	}
}

func TestAlipayVerifyCallback(t *testing.T) {
	pub, err := parseRSAPublicKey(alipayTestPub)
	if err != nil {
		t.Fatalf("公钥解析失败: %v", err)
	}
	msg := "app_id=2021001&charset=utf-8&format=JSON&method=alipay.trade.page.pay&timestamp=2026-08-16 12:00:00&version=1.0"
	if err := rsa2Verify(pub, []byte(msg), alipayGoldenSign); err != nil {
		t.Fatalf("验签失败: %v", err)
	}
	if err := rsa2Verify(pub, []byte(msg+"tampered"), alipayGoldenSign); err == nil {
		t.Fatal("篡改内容应验签失败")
	}
}

func TestValidateConfig(t *testing.T) {
	if err := NewEpay().ValidateConfig(json.RawMessage(`{"pid":"1","key":"k"}`)); err != nil {
		t.Fatalf("epay 合法凭据校验失败: %v", err)
	}
	if err := NewEpay().ValidateConfig(json.RawMessage(`{}`)); err == nil {
		t.Fatal("epay 空凭据应校验失败")
	}
	alipayCfg, _ := json.Marshal(alipayConfig{AppID: "1", PrivateKey: alipayTestPriv, AlipayPublicKey: alipayTestPub})
	if err := NewAlipay().ValidateConfig(alipayCfg); err != nil {
		t.Fatalf("alipay 合法凭据校验失败: %v", err)
	}
}

// ── 金额转换（分 ↔ 元，禁止 float）───────────────────────────

func TestCentsToYuan(t *testing.T) {
	cases := map[int64]string{1234: "12.34", 100: "1.00", 5: "0.05", 0: "0.00", -1234: "-12.34"}
	for in, want := range cases {
		if got := centsToYuan(in); got != want {
			t.Fatalf("centsToYuan(%d) = %s, want %s", in, got, want)
		}
	}
}

func TestYuanToCents(t *testing.T) {
	cases := map[string]int64{"12.34": 1234, "1": 100, "0.05": 5, "12": 1200, "12.3": 1230}
	for in, want := range cases {
		got, err := yuanToCents(in)
		if err != nil || got != want {
			t.Fatalf("yuanToCents(%q) = %d,%v want %d", in, got, err, want)
		}
	}
	if _, err := yuanToCents("12.345"); err == nil {
		t.Fatal("三位小数应拒绝")
	}
}
