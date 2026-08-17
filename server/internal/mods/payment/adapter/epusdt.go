package adapter

// epusdt 适配器（P2-09 T1）：GMPay 协议（github.com/GMWalletApp/epusdt 自托管 USDT 网关）。
//
// 协议（1.x EpuSdtDriver 沉淀 + dujiao-next gateway/epusdt 对拍）：
//   - 下单：POST {api_url}/payments/gmpay/v1/order/create-transaction
//     参数 pid/order_id/currency/amount(法币元两位小数)/notify_url/redirect_url/name/network/token
//   - 签名：剔 signature + 空值 → key ASCII 字典序 → k=v& 拼接 → HMAC-SHA256(secret) 小写 hex
//   - 回调：JSON POST；status==2 支付成功；重签对比；amount 为法币元（与下单同口径，零换算）
//   - 应答：纯文本 "ok"（Acker 能力位；管线 JSON 兜底不适用于本协议）
//
// 金额纪律：全链 int64 分；出口一次性格式化两位小数字符串（绝不过 float64 变量——
// dujiao 踩坑记录：签名串 float 必须去尾零，直接构造字符串则无此问题）。
// USDT 实收（actual_amount）不参与金额核对，仅随 Raw 落审计。

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// epusdtConfig 渠道凭据（解密后 JSON；admin 支付渠道配置）。
type epusdtConfig struct {
	APIURL    string `json:"api_url"`    // 网关根（如 https://epay.example.com）
	PID       string `json:"pid"`        // 商户 ID
	SecretKey string `json:"secret_key"` // HMAC 密钥
	Currency  string `json:"currency"`   // 法币：cny（默认）/usd
	Token     string `json:"token"`      // 支付代币（默认 USDT）
	Network   string `json:"network"`    // 链（默认 TRC20）
}

// EpusdtAdapter GMPay 协议适配器（无状态——凭据逐调用传入）。
type EpusdtAdapter struct{}

// NewEpusdt 构造。
func NewEpusdt() *EpusdtAdapter { return &EpusdtAdapter{} }

// Type 渠道驱动名。
func (a *EpusdtAdapter) Type() string { return "epusdt" }

// epusdtSign GMPay HMAC-SHA256 签名（剔 signature/空值 → ASCII 序 → k=v& → HMAC 小写 hex）。
func epusdtSign(params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "signature" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(params[k])
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(b.String()))
	return hex.EncodeToString(mac.Sum(nil))
}

// epusdtVerifySign 常数时间验签（严格小写 hex 比对——协议口径，1.x hash_equals 同）。
func epusdtVerifySign(params map[string]string, secret, provided string) bool {
	return constantTimeEq(provided, epusdtSign(params, secret))
}

// ValidateConfig 校验凭据必填。
func (a *EpusdtAdapter) ValidateConfig(cfg json.RawMessage) error {
	var c epusdtConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return fmt.Errorf("epusdt: 凭据格式错误: %w", err)
	}
	if c.APIURL == "" || c.PID == "" || c.SecretKey == "" {
		return fmt.Errorf("epusdt: api_url/pid/secret_key 必填")
	}
	return nil
}

// epusdtCreateReply 下单响应（GMPay）。
type epusdtCreateReply struct {
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
	Data       struct {
		TradeID    string `json:"trade_id"`
		PaymentURL string `json:"payment_url"`
		Amount     string `json:"amount"`
		Token      string `json:"token"`
		Network    string `json:"network"`
	} `json:"data"`
}

// CreatePayment 下单（redirect 到网关收银台）。
func (a *EpusdtAdapter) CreatePayment(ctx context.Context, req port.CreatePaymentRequest) (*port.RedirectInfo, error) {
	var c epusdtConfig
	if err := json.Unmarshal(req.Config, &c); err != nil {
		return nil, fmt.Errorf("epusdt: 凭据格式错误: %w", err)
	}
	if c.APIURL == "" || c.PID == "" || c.SecretKey == "" {
		return nil, fmt.Errorf("epusdt: api_url/pid/secret_key 必填")
	}
	// 币种快照（P2-09 T2）：跨币路径服务端已换算——ChargedUnits/ChargedCurrency
	// 为权威金额；同币直收回落 cfg currency（默认 cny）
	currency := c.Currency
	if currency == "" {
		currency = "cny"
	}
	chargeUnits := int64(req.Amount)
	if req.ChargedUnits > 0 {
		currency = strings.ToLower(req.ChargedCurrency)
		chargeUnits = req.ChargedUnits
	}
	token := c.Token
	if token == "" {
		token = "USDT"
	}
	network := c.Network
	if network == "" {
		network = "TRC20"
	}
	params := map[string]string{
		"pid":      c.PID,
		"order_id": req.OrderNo,
		"currency": currency,
		// GMPay cny/usd 均两位小数（最小单位=分/美分同构）；快照口径直接出口
		"amount":       centsToYuan(chargeUnits),
		"notify_url":   req.NotifyBaseURL,
		"redirect_url": req.ReturnURL,
		"name":         req.Subject,
		"network":      network,
		"token":        token,
	}
	params["signature"] = epusdtSign(params, c.SecretKey)

	body, _ := json.Marshal(params)
	url := strings.TrimRight(c.APIURL, "/") + "/payments/gmpay/v1/order/create-transaction"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("epusdt: 下单请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("epusdt: 下单失败 HTTP %d", resp.StatusCode)
	}
	var reply epusdtCreateReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("epusdt: 下单响应解析失败: %w", err)
	}
	if reply.StatusCode != 200 || reply.Data.PaymentURL == "" {
		return nil, fmt.Errorf("epusdt: 下单被网关拒绝: %s", reply.Message)
	}
	payload, _ := json.Marshal(map[string]string{"url": reply.Data.PaymentURL})
	return &port.RedirectInfo{Type: "redirect", Payload: payload}, nil
}

// VerifyCallback 验签回调（JSON→form map 已由管线展平；重签对比 + status==2）。
func (a *EpusdtAdapter) VerifyCallback(form map[string]string, cfg json.RawMessage) (*port.CallbackFact, error) {
	var c epusdtConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return nil, fmt.Errorf("epusdt: 凭据格式错误: %w", err)
	}
	sig, ok := form["signature"]
	if !ok || sig == "" {
		return nil, fmt.Errorf("epusdt: 缺 signature")
	}
	if !epusdtVerifySign(form, c.SecretKey, sig) {
		return nil, fmt.Errorf("epusdt: 验签失败")
	}
	success := form["status"] == "2"
	fiat := strings.ToUpper(c.Currency)
	if fiat == "" {
		fiat = "CNY"
	}
	fact := &port.CallbackFact{
		Provider:       "epusdt",
		ChannelOrderNo: form["trade_id"],
		OrderNo:        form["order_id"],
		Currency:       fiat,
		Success:        success,
		Raw:            jsonMust(form),
	}
	if success {
		cents, err := yuanToCents(form["amount"])
		if err != nil {
			return nil, fmt.Errorf("epusdt: 金额解析失败: %w", err)
		}
		fact.Amount = money.Cents(cents)
	}
	return fact, nil
}

// SuccessAck GMPay 回调应答（纯文本 ok；dujiao EpusdtCallbackSuccess 同口径）。
func (a *EpusdtAdapter) SuccessAck() string { return "ok" }
