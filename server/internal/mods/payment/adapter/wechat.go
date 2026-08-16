package adapter

// 微信支付 Native v2 adapter（/pay/unifiedorder XML 接口）。
//
// 下单签名：对「按 key 字典序拼接的非空参数」 + "&key=" + api_key，MD5（或 HMAC-SHA256），大写 hex。
// total_fee 单位为「分」直传。回调为 XML（<xml>），验签同样重算比对。
// 返回 code_url（二维码，type=qrcode）。

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/httpx"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// wechatConfig 微信支付渠道凭据。
type wechatConfig struct {
	AppID     string `json:"app_id"`
	MchID     string `json:"mch_id"`
	APIKey    string `json:"api_key"`
	SignType  string `json:"sign_type"` // MD5 | HMAC-SHA256，默认 MD5
	NotifyURL string `json:"notify_url"`
}

// WechatAdapter 微信支付适配器。
type WechatAdapter struct{}

// NewWechat 构造。
func NewWechat() *WechatAdapter { return &WechatAdapter{} }

// Type 渠道驱动名。
func (a *WechatAdapter) Type() string { return "wechat" }

// ValidateConfig 校验凭据。
func (a *WechatAdapter) ValidateConfig(cfg json.RawMessage) error {
	var c wechatConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return fmt.Errorf("wechat: 凭据格式错误: %w", err)
	}
	if c.AppID == "" || c.MchID == "" || c.APIKey == "" {
		return fmt.Errorf("wechat: app_id/mch_id/api_key 必填")
	}
	return nil
}

// sign 微信签名：sortParams(排除 sign) + "&key=" + key。
func (c wechatConfig) sign(params map[string]string) string {
	base := sortParams(params, "sign") + "&key=" + c.APIKey
	if strings.EqualFold(c.SignType, "HMAC-SHA256") {
		return hmacSHA256Upper(c.APIKey, base)
	}
	return md5Upper(base)
}

// CreatePayment 统一下单（Native），返回 code_url。
func (a *WechatAdapter) CreatePayment(ctx context.Context, req port.CreatePaymentRequest) (*port.RedirectInfo, error) {
	var c wechatConfig
	if err := json.Unmarshal(req.Config, &c); err != nil {
		return nil, fmt.Errorf("wechat: 凭据格式错误: %w", err)
	}
	notifyURL := firstNonEmpty(c.NotifyURL, req.NotifyBaseURL)

	params := map[string]string{
		"appid":            c.AppID,
		"mch_id":           c.MchID,
		"nonce_str":        nonceStr(16),
		"body":             req.Subject,
		"out_trade_no":     req.OrderNo,
		"total_fee":        fmt.Sprintf("%d", int64(req.Amount)),
		"spbill_create_ip": "127.0.0.1",
		"notify_url":       notifyURL,
		"trade_type":       "NATIVE",
	}
	params["sign"] = c.sign(params)

	body := toXML(params)
	resp, err := postXML(ctx, "https://api.mch.weixin.qq.com/pay/unifiedorder", body)
	if err != nil {
		return nil, err
	}
	var result struct {
		ReturnCode string `xml:"return_code"`
		ReturnMsg  string `xml:"return_msg"`
		ResultCode string `xml:"result_code"`
		ErrCode    string `xml:"err_code"`
		ErrCodeDes string `xml:"err_code_des"`
		CodeURL    string `xml:"code_url"`
	}
	if err := xml.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("wechat: 下单响应解析失败: %w", err)
	}
	if result.ReturnCode != "SUCCESS" {
		return nil, fmt.Errorf("wechat: 下单失败 %s: %s", result.ReturnCode, result.ReturnMsg)
	}
	if result.ResultCode != "SUCCESS" {
		return nil, fmt.Errorf("wechat: 下单失败 %s: %s", result.ErrCode, result.ErrCodeDes)
	}
	if result.CodeURL == "" {
		return nil, fmt.Errorf("wechat: 下单未返回 code_url")
	}
	return &port.RedirectInfo{
		Type:    "qrcode",
		Payload: jsonMust(map[string]string{"code_url": result.CodeURL}),
	}, nil
}

// VerifyCallback 微信支付结果通知验签（XML body 已解析为 map）。
func (a *WechatAdapter) VerifyCallback(form map[string]string, cfg json.RawMessage) (*port.CallbackFact, error) {
	var c wechatConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return nil, fmt.Errorf("wechat: 凭据格式错误: %w", err)
	}
	sign := form["sign"]
	if sign == "" {
		return nil, fmt.Errorf("wechat: 缺 sign")
	}
	expect := c.sign(form)
	if !constantTimeEq(strings.ToUpper(sign), expect) {
		return nil, fmt.Errorf("wechat: 验签失败")
	}
	// 微信 total_fee 单位即「分」直传
	var amount int64
	if _, err := fmt.Sscanf(form["total_fee"], "%d", &amount); err != nil {
		return nil, fmt.Errorf("wechat: 金额解析失败: %w", err)
	}
	success := form["result_code"] == "SUCCESS" && form["return_code"] == "SUCCESS"
	return &port.CallbackFact{
		Provider:       "wechat",
		ChannelOrderNo: form["transaction_id"],
		OrderNo:        form["out_trade_no"],
		Amount:         money.Cents(amount),
		Currency:       "CNY",
		Success:        success,
		Raw:            jsonMust(form),
	}, nil
}

// ── XML 工具 ──

// toXML 构造微信下单 XML（参数排序后拼接）。
func toXML(params map[string]string) []byte {
	var b strings.Builder
	b.WriteString("<xml>")
	for _, k := range sortedKeys(params) {
		b.WriteString("<")
		b.WriteString(k)
		b.WriteString(">")
		b.WriteString(xmlEscape(params[k]))
		b.WriteString("</")
		b.WriteString(k)
		b.WriteString(">")
	}
	b.WriteString("</xml>")
	return []byte(b.String())
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// postXML 安全出站 POST XML（SSRF 校验）。
func postXML(ctx context.Context, rawURL string, body []byte) ([]byte, error) {
	if err := httpx.ValidateURL(rawURL); err != nil {
		return nil, err
	}
	client := httpx.NewSafeClient(15 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml")
	req.Header.Set("User-Agent", httpx.UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
