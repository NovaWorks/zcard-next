package adapter

// 易支付（epay 协议族，兼容 codepay/okpay 同族）adapter。
//
// 签名口径（1.x 沉淀）：sign = MD5(sortParams(排除 sign/sign_type/空值) + key)，小写 hex。
// 回调为 GET/POST 表单；验签重算 MD5 后常数时间比对。
// 金额：元（分→元两位小数）。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// epayConfig 易支付渠道凭据（解密后 JSON）。
type epayConfig struct {
	PID       string `json:"pid"`        // 商户号
	Key       string `json:"key"`        // 商户密钥
	APIURL    string `json:"api_url"`    // 下单网关（默认 https://pay.xxx.com/submit.php）
	ReturnURL string `json:"return_url"` // 同步跳转
	NotifyURL string `json:"notify_url"` // 异步通知
}

// EpayAdapter 易支付适配器。
type EpayAdapter struct{}

// NewEpay 构造。
func NewEpay() *EpayAdapter { return &EpayAdapter{} }

// Type 渠道驱动名。
func (a *EpayAdapter) Type() string { return "epay" }

// ValidateConfig 校验凭据必填。
func (a *EpayAdapter) ValidateConfig(cfg json.RawMessage) error {
	var c epayConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return fmt.Errorf("epay: 凭据格式错误: %w", err)
	}
	if c.PID == "" || c.Key == "" {
		return fmt.Errorf("epay: pid/key 必填")
	}
	return nil
}

// CreatePayment 构造跳转参数（type=params，前端 POST 到 api_url）。
func (a *EpayAdapter) CreatePayment(_ context.Context, req port.CreatePaymentRequest) (*port.RedirectInfo, error) {
	var c epayConfig
	if err := json.Unmarshal(req.Config, &c); err != nil {
		return nil, fmt.Errorf("epay: 凭据格式错误: %w", err)
	}
	if c.PID == "" || c.Key == "" {
		return nil, fmt.Errorf("epay: pid/key 必填")
	}
	apiURL := c.APIURL
	if apiURL == "" {
		apiURL = "https://pay.epay.com/submit.php"
	}
	notifyURL := req.NotifyBaseURL
	if c.NotifyURL != "" {
		notifyURL = c.NotifyURL
	}
	returnURL := req.ReturnURL
	if c.ReturnURL != "" {
		returnURL = c.ReturnURL
	}

	params := map[string]string{
		"pid":          c.PID,
		"type":         "alipay",
		"out_trade_no": req.OrderNo,
		"notify_url":   notifyURL,
		"return_url":   returnURL,
		"name":         req.Subject,
		"money":        centsToYuan(int64(req.Amount)),
		"sign_type":    "MD5",
	}
	params["sign"] = md5Hex(sortParams(params, "sign", "sign_type") + c.Key)

	payload, _ := json.Marshal(map[string]any{"url": apiURL, "method": "POST", "params": params})
	return &port.RedirectInfo{Type: "params", Payload: payload}, nil
}

// VerifyCallback 验签回调表单。
func (a *EpayAdapter) VerifyCallback(form map[string]string, cfg json.RawMessage) (*port.CallbackFact, error) {
	var c epayConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return nil, fmt.Errorf("epay: 凭据格式错误: %w", err)
	}
	sign, ok := form["sign"]
	if !ok || sign == "" {
		return nil, fmt.Errorf("epay: 缺 sign")
	}
	// 重算：排除 sign/sign_type/空值
	expect := md5Hex(sortParams(form, "sign", "sign_type") + c.Key)
	if !constantTimeEq(strings.ToLower(sign), expect) {
		return nil, fmt.Errorf("epay: 验签失败")
	}
	amount, err := yuanToCents(form["money"])
	if err != nil {
		return nil, fmt.Errorf("epay: 金额解析失败: %w", err)
	}
	success := form["trade_status"] == "TRADE_SUCCESS"
	return &port.CallbackFact{
		Provider:       "epay",
		ChannelOrderNo: form["trade_no"],
		OrderNo:        form["out_trade_no"],
		Amount:         money.Cents(amount),
		Currency:       "CNY",
		Success:        success,
		Raw:            jsonMust(form),
	}, nil
}
