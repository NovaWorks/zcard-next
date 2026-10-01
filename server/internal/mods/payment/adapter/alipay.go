package adapter

// 支付宝 adapter（RSA2 / SHA256withRSA）。
//
// 下单：alipay.trade.page.pay（网页支付，GET 跳转收银台）。请求签名包含 sign_type；回调验签排除 sign/sign_type。
// 用商户私钥 RSA2 签名后 base64。回调验签用支付宝公钥 RSA2 verify。
// 金额：元（分→元两位小数）。

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/currencyunit"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/httpx"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// alipayConfig 支付宝渠道凭据。
type alipayConfig struct {
	AppID           string `json:"app_id"`
	PrivateKey      string `json:"private_key"`       // 商户私钥 PEM（RSA2）
	AlipayPublicKey string `json:"alipay_public_key"` // 支付宝公钥 PEM
	Gateway         string `json:"gateway"`           // 默认 https://openapi.alipay.com/gateway.do
	NotifyURL       string `json:"notify_url"`
	ReturnURL       string `json:"return_url"`
}

// AlipayAdapter 支付宝适配器。
type AlipayAdapter struct{ client *http.Client }

// NewAlipay 构造。
func NewAlipay() *AlipayAdapter { return &AlipayAdapter{client: httpx.NewSafeClient(15 * time.Second)} }

// Type 渠道驱动名。
func (a *AlipayAdapter) Type() string { return "alipay" }

func (a *AlipayAdapter) SuccessAck() string { return "success" }

var alipayLocation = time.FixedZone("CST", 8*60*60)

func parseAlipayConfig(cfg json.RawMessage) (alipayConfig, error) {
	var c alipayConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return c, fmt.Errorf("alipay: 凭据格式错误")
	}
	c.AppID = strings.TrimSpace(c.AppID)
	c.Gateway = strings.TrimSpace(c.Gateway)
	if c.AppID == "" || strings.TrimSpace(c.PrivateKey) == "" || strings.TrimSpace(c.AlipayPublicKey) == "" {
		return c, fmt.Errorf("alipay: app_id/private_key/alipay_public_key 必填")
	}
	if c.Gateway == "" {
		c.Gateway = "https://openapi.alipay.com/gateway.do"
	}
	u, err := url.Parse(c.Gateway)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, fmt.Errorf("alipay: 网关须为不含查询参数的完整 HTTP(S) 地址")
	}
	return c, nil
}

// ValidateConfig 校验凭据与密钥格式。
func (a *AlipayAdapter) ValidateConfig(cfg json.RawMessage) error {
	c, err := parseAlipayConfig(cfg)
	if err != nil {
		return err
	}
	if _, err := parseRSAPrivateKey(c.PrivateKey); err != nil {
		return fmt.Errorf("alipay: 私钥解析失败: %w", err)
	}
	if _, err := parseRSAPublicKey(c.AlipayPublicKey); err != nil {
		return fmt.Errorf("alipay: 公钥解析失败: %w", err)
	}
	return nil
}

// CreatePayment 构造网页支付跳转 URL（type=redirect）。
func (a *AlipayAdapter) CreatePayment(_ context.Context, req port.CreatePaymentRequest) (*port.RedirectInfo, error) {
	c, err := parseAlipayConfig(req.Config)
	if err != nil {
		return nil, err
	}
	if req.Amount <= 0 || !money.ValidCents(int64(req.Amount)) || req.ChargedUnits != 0 || (req.ChargedCurrency != "" && req.ChargedCurrency != "CNY") {
		return nil, fmt.Errorf("alipay: 仅支持人民币正数金额")
	}
	orderNo := firstNonEmpty(req.GatewayOrderRef, req.OrderNo)
	if strings.TrimSpace(orderNo) == "" {
		return nil, fmt.Errorf("alipay: 商户订单号必填")
	}

	bizContent, _ := json.Marshal(map[string]any{
		"out_trade_no": orderNo,
		"total_amount": centsToYuan(int64(req.Amount)),
		"subject":      req.Subject,
		"product_code": "FAST_INSTANT_TRADE_PAY",
	})

	params := map[string]string{
		"app_id":      c.AppID,
		"method":      "alipay.trade.page.pay",
		"format":      "JSON",
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().In(alipayLocation).Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"notify_url":  firstNonEmpty(c.NotifyURL, req.NotifyBaseURL),
		"return_url":  firstNonEmpty(c.ReturnURL, req.ReturnURL),
		"biz_content": string(bizContent),
	}

	priv, err := parseRSAPrivateKey(c.PrivateKey)
	if err != nil {
		return nil, err
	}
	sign, err := rsa2Sign(priv, []byte(sortParams(params, "sign")))
	if err != nil {
		return nil, fmt.Errorf("alipay: 签名失败: %w", err)
	}
	params["sign"] = sign

	q := url.Values{}
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	return &port.RedirectInfo{
		Type:    "redirect",
		Payload: jsonMust(map[string]string{"url": c.Gateway + "?" + q.Encode()}),
	}, nil
}

// VerifyCallback 支付宝异步通知验签（表单）。
func (a *AlipayAdapter) VerifyCallback(form map[string]string, cfg json.RawMessage) (*port.CallbackFact, error) {
	c, err := parseAlipayConfig(cfg)
	if err != nil {
		return nil, err
	}
	sign := form["sign"]
	if sign == "" {
		return nil, fmt.Errorf("alipay: 缺 sign")
	}
	pub, err := parseRSAPublicKey(c.AlipayPublicKey)
	if err != nil {
		return nil, err
	}
	if err := rsa2Verify(pub, []byte(sortParams(form, "sign", "sign_type")), sign); err != nil {
		return nil, fmt.Errorf("alipay: 验签失败: %w", err)
	}
	if form["sign_type"] != "RSA2" || form["app_id"] != c.AppID {
		return nil, fmt.Errorf("alipay: 回调签名类型或 APPID 不匹配")
	}
	if strings.TrimSpace(form["out_trade_no"]) == "" || strings.TrimSpace(form["trade_no"]) == "" {
		return nil, fmt.Errorf("alipay: 回调缺少订单号")
	}
	tradeStatus := form["trade_status"]
	success := tradeStatus == "TRADE_SUCCESS" || tradeStatus == "TRADE_FINISHED"
	amount, err := alipayAmount(form["total_amount"])
	if err != nil {
		return nil, fmt.Errorf("alipay: 金额解析失败: %w", err)
	}
	return &port.CallbackFact{
		Provider:       "alipay",
		ChannelOrderNo: form["trade_no"],
		OrderNo:        form["out_trade_no"],
		Amount:         money.Cents(amount),
		Currency:       "CNY",
		Success:        success,
		Raw:            jsonMust(form),
	}, nil
}

// QueryPayment 主动查单（alipay.trade.query）——签名 + POST 网关，解析 trade_status。
func (a *AlipayAdapter) QueryPayment(ctx context.Context, gatewayOrderNo string, cfg json.RawMessage) (*port.CallbackFact, error) {
	return a.queryPayment(ctx, "trade_no", gatewayOrderNo, cfg)
}

// QueryPaymentByOrderNo 支持回调丢失时按商户订单号补单。
func (a *AlipayAdapter) QueryPaymentByOrderNo(ctx context.Context, orderNo string, cfg json.RawMessage) (*port.CallbackFact, error) {
	return a.queryPayment(ctx, "out_trade_no", orderNo, cfg)
}

func (a *AlipayAdapter) queryPayment(ctx context.Context, key, orderNo string, cfg json.RawMessage) (*port.CallbackFact, error) {
	c, err := parseAlipayConfig(cfg)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(orderNo) == "" {
		return nil, fmt.Errorf("alipay: 查单订单号必填")
	}
	bizContent, _ := json.Marshal(map[string]string{key: orderNo})
	params := map[string]string{
		"app_id":      c.AppID,
		"method":      "alipay.trade.query",
		"format":      "JSON",
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().In(alipayLocation).Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"biz_content": string(bizContent),
	}
	priv, err := parseRSAPrivateKey(c.PrivateKey)
	if err != nil {
		return nil, err
	}
	sign, err := rsa2Sign(priv, []byte(sortParams(params, "sign")))
	if err != nil {
		return nil, err
	}
	params["sign"] = sign

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	resp, err := a.postForm(ctx, c.Gateway, form)
	if err != nil {
		return nil, err
	}
	var body struct {
		Response      json.RawMessage `json:"alipay_trade_query_response"`
		ErrorResponse json.RawMessage `json:"error_response"`
		Sign          string          `json:"sign"`
	}
	if err := json.Unmarshal(resp, &body); err != nil {
		return nil, fmt.Errorf("alipay: 查单响应解析失败: %w", err)
	}
	raw := body.Response
	if len(raw) == 0 {
		raw = body.ErrorResponse
	}
	var result struct {
		Code        string `json:"code"`
		SubCode     string `json:"sub_code"`
		TradeStatus string `json:"trade_status"`
		TradeNo     string `json:"trade_no"`
		OutTradeNo  string `json:"out_trade_no"`
		TotalAmount string `json:"total_amount"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Code == "" {
		return nil, fmt.Errorf("alipay: 查单响应缺少有效结果")
	}
	if result.Code != "10000" {
		return nil, fmt.Errorf("alipay: 查单失败 code=%s sub_code=%s", result.Code, result.SubCode)
	}
	if len(body.Response) == 0 || body.Sign == "" {
		return nil, fmt.Errorf("alipay: 查单响应缺少签名")
	}
	pub, err := parseRSAPublicKey(c.AlipayPublicKey)
	if err != nil {
		return nil, err
	}
	// 必须验证响应中的原始 JSON 字节，重新序列化会改变签名内容。
	if err := rsa2Verify(pub, raw, body.Sign); err != nil {
		return nil, fmt.Errorf("alipay: 查单响应验签失败: %w", err)
	}
	if result.TradeNo == "" || result.OutTradeNo == "" || (key == "trade_no" && result.TradeNo != orderNo) || (key == "out_trade_no" && result.OutTradeNo != orderNo) {
		return nil, fmt.Errorf("alipay: 查单响应订单号不匹配")
	}
	amount, err := alipayAmount(result.TotalAmount)
	if err != nil {
		return nil, err
	}
	success := result.TradeStatus == "TRADE_SUCCESS" || result.TradeStatus == "TRADE_FINISHED"
	return &port.CallbackFact{
		Provider:       "alipay",
		ChannelOrderNo: result.TradeNo,
		OrderNo:        result.OutTradeNo,
		Amount:         money.Cents(amount),
		Currency:       "CNY",
		Success:        success,
		Raw:            json.RawMessage(resp),
	}, nil
}

// ── RSA2 工具 ──

func alipayAmount(value string) (int64, error) {
	amount, err := currencyunit.ParseChargeAmount(value, currencyunit.ChargeUnit{Precision: 2, Step: 1})
	if err != nil || amount <= 0 || !money.ValidCents(amount) {
		return 0, fmt.Errorf("alipay: 金额无效（需人民币正数，最多两位有效小数）")
	}
	return amount, nil
}

func rsa2Sign(priv *rsa.PrivateKey, data []byte) (string, error) {
	h := sha256Sum(data)
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, h)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

func rsa2Verify(pub *rsa.PublicKey, data []byte, sigBase64 string) error {
	sig, err := base64.StdEncoding.DecodeString(sigBase64)
	if err != nil {
		return err
	}
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sha256Sum(data), sig)
}

func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	der, err := alipayKeyDER(pemStr, "PRIVATE KEY", "RSA PRIVATE KEY")
	if err != nil {
		return nil, err
	}
	// PKCS8
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
	}
	// PKCS1
	if k, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return k, nil
	}
	return nil, fmt.Errorf("私钥格式不支持（需 PKCS1/PKCS8 RSA）")
}

func parseRSAPublicKey(pemStr string) (*rsa.PublicKey, error) {
	der, err := alipayKeyDER(pemStr, "PUBLIC KEY", "RSA PUBLIC KEY")
	if err != nil {
		return nil, err
	}
	// PKIX
	if k, err := x509.ParsePKIXPublicKey(der); err == nil {
		if rk, ok := k.(*rsa.PublicKey); ok {
			return rk, nil
		}
	}
	// PKCS1
	if k, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return k, nil
	}
	return nil, fmt.Errorf("公钥格式不支持（需 PKIX/PKCS1 RSA）")
}

var alipayPEMPattern = regexp.MustCompile(`(?s)^-----BEGIN ([A-Z0-9 ]+)-----(.*?)-----END ([A-Z0-9 ]+)-----$`)

// 支持密钥工具的裸 Base64、标准 PEM，以及单行输入造成的换行丢失。
// 不把证书或加密私钥当作普通 RSA 密钥，避免配置看似成功却无法签名。
func alipayKeyDER(value string, types ...string) ([]byte, error) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "\ufeff"))
	value = strings.TrimSpace(strings.NewReplacer(`\r\n`, "\n", `\n`, "\n", `\r`, "\n").Replace(value))
	if strings.Contains(value, "-----") {
		match := alipayPEMPattern.FindStringSubmatch(value)
		if match == nil || match[1] != match[3] {
			return nil, fmt.Errorf("密钥 PEM 格式错误，请粘贴完整密钥或裸 Base64")
		}
		if match[1] == "CERTIFICATE" {
			return nil, fmt.Errorf("当前渠道使用普通公钥模式，请填写支付宝公钥，不能填写公钥证书")
		}
		validType := false
		for _, typ := range types {
			validType = validType || match[1] == typ
		}
		if !validType {
			return nil, fmt.Errorf("密钥类型不匹配，请使用未加密的 RSA 密钥")
		}
		value = match[2]
	}
	der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(value), ""))
	if err != nil || len(der) == 0 {
		return nil, fmt.Errorf("密钥 Base64 格式错误，请检查内容是否完整")
	}
	return der, nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// postForm 安全出站 POST 表单（SSRF 校验 + 超时 + 脱敏日志），经 httpx.NewSafeClient。
func (a *AlipayAdapter) postForm(ctx context.Context, rawURL string, form url.Values) ([]byte, error) {
	if err := httpx.ValidateURL(rawURL); err != nil {
		return nil, err
	}
	client := a.client
	if client == nil {
		client = httpx.NewSafeClient(15 * time.Second)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", httpx.UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alipay: 查单 HTTP 状态异常: %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
