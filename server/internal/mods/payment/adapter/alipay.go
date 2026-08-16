package adapter

// 支付宝 adapter（RSA2 / SHA256withRSA）。
//
// 下单：alipay.trade.page.pay（网页支付，GET 跳转收银台）。签名串 = sortParams(排除 sign/sign_type)
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
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

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
type AlipayAdapter struct{}

// NewAlipay 构造。
func NewAlipay() *AlipayAdapter { return &AlipayAdapter{} }

// Type 渠道驱动名。
func (a *AlipayAdapter) Type() string { return "alipay" }

// ValidateConfig 校验凭据与密钥格式。
func (a *AlipayAdapter) ValidateConfig(cfg json.RawMessage) error {
	var c alipayConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return fmt.Errorf("alipay: 凭据格式错误: %w", err)
	}
	if c.AppID == "" || c.PrivateKey == "" || c.AlipayPublicKey == "" {
		return fmt.Errorf("alipay: app_id/private_key/alipay_public_key 必填")
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
	var c alipayConfig
	if err := json.Unmarshal(req.Config, &c); err != nil {
		return nil, fmt.Errorf("alipay: 凭据格式错误: %w", err)
	}
	gateway := c.Gateway
	if gateway == "" {
		gateway = "https://openapi.alipay.com/gateway.do"
	}

	bizContent, _ := json.Marshal(map[string]any{
		"out_trade_no": req.OrderNo,
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
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"notify_url":  firstNonEmpty(c.NotifyURL, req.NotifyBaseURL),
		"return_url":  firstNonEmpty(c.ReturnURL, req.ReturnURL),
		"biz_content": string(bizContent),
	}

	priv, err := parseRSAPrivateKey(c.PrivateKey)
	if err != nil {
		return nil, err
	}
	sign, err := rsa2Sign(priv, []byte(sortParams(params, "sign", "sign_type")))
	if err != nil {
		return nil, fmt.Errorf("alipay: 签名失败: %w", err)
	}
	params["sign"] = sign

	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	return &port.RedirectInfo{
		Type:    "redirect",
		Payload: jsonMust(map[string]string{"url": gateway + "?" + q.Encode()}),
	}, nil
}

// VerifyCallback 支付宝异步通知验签（表单）。
func (a *AlipayAdapter) VerifyCallback(form map[string]string, cfg json.RawMessage) (*port.CallbackFact, error) {
	var c alipayConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return nil, fmt.Errorf("alipay: 凭据格式错误: %w", err)
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
	tradeStatus := form["trade_status"]
	success := tradeStatus == "TRADE_SUCCESS" || tradeStatus == "TRADE_FINISHED"
	amount, err := yuanToCents(form["total_amount"])
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
	var c alipayConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return nil, fmt.Errorf("alipay: 凭据格式错误: %w", err)
	}
	bizContent, _ := json.Marshal(map[string]any{"out_trade_no": gatewayOrderNo})
	params := map[string]string{
		"app_id":      c.AppID,
		"method":      "alipay.trade.query",
		"format":      "JSON",
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"biz_content": string(bizContent),
	}
	priv, err := parseRSAPrivateKey(c.PrivateKey)
	if err != nil {
		return nil, err
	}
	sign, err := rsa2Sign(priv, []byte(sortParams(params, "sign", "sign_type")))
	if err != nil {
		return nil, err
	}
	params["sign"] = sign

	gateway := c.Gateway
	if gateway == "" {
		gateway = "https://openapi.alipay.com/gateway.do"
	}
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	resp, err := postForm(ctx, gateway, form)
	if err != nil {
		return nil, err
	}
	var body struct {
		AlipayTradeQueryResponse struct {
			Code        string `json:"code"`
			TradeStatus string `json:"trade_status"`
			TradeNo     string `json:"trade_no"`
			OutTradeNo  string `json:"out_trade_no"`
			TotalAmount string `json:"total_amount"`
		} `json:"alipay_trade_query_response"`
		Sign string `json:"sign"`
	}
	if err := json.Unmarshal(resp, &body); err != nil {
		return nil, fmt.Errorf("alipay: 查单响应解析失败: %w", err)
	}
	if body.AlipayTradeQueryResponse.Code != "10000" {
		return nil, fmt.Errorf("alipay: 查单失败 code=%s", body.AlipayTradeQueryResponse.Code)
	}
	amount, _ := yuanToCents(body.AlipayTradeQueryResponse.TotalAmount)
	success := body.AlipayTradeQueryResponse.TradeStatus == "TRADE_SUCCESS" || body.AlipayTradeQueryResponse.TradeStatus == "TRADE_FINISHED"
	return &port.CallbackFact{
		Provider:       "alipay",
		ChannelOrderNo: body.AlipayTradeQueryResponse.TradeNo,
		OrderNo:        body.AlipayTradeQueryResponse.OutTradeNo,
		Amount:         money.Cents(amount),
		Currency:       "CNY",
		Success:        success,
		Raw:            json.RawMessage(resp),
	}, nil
}

// ── RSA2 工具 ──

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
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("private key PEM 解析失败")
	}
	// PKCS8
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
	}
	// PKCS1
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, fmt.Errorf("私钥格式不支持（需 PKCS1/PKCS8 RSA）")
}

func parseRSAPublicKey(pemStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("public key PEM 解析失败")
	}
	// PKIX
	if k, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PublicKey); ok {
			return rk, nil
		}
	}
	// PKCS1
	if k, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, fmt.Errorf("公钥格式不支持（需 PKIX/PKCS1 RSA）")
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// postForm 安全出站 POST 表单（SSRF 校验 + 超时 + 脱敏日志），经 httpx.NewSafeClient。
func postForm(ctx context.Context, rawURL string, form url.Values) ([]byte, error) {
	if err := httpx.ValidateURL(rawURL); err != nil {
		return nil, err
	}
	client := httpx.NewSafeClient(15 * time.Second)
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
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
