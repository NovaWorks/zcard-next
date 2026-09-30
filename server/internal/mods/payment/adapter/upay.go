package adapter

// UPAY_PRO native protocol, verified against auvqiao/UPAY_PRO
// a232783682f9617bcc772e1e2c404ae809475c84 (web/function.go and cron/cron.go).
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/currencyunit"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/shopspring/decimal"
)

type UpayConfig struct {
	APIURL     string   `json:"api_url"`
	SecretKey  string   `json:"secret_key"`
	TradeType  string   `json:"trade_type,omitempty"`
	TradeTypes []string `json:"trade_types,omitempty"`
	Timeout    int64    `json:"timeout"`
}

var upayTrades = []port.ConfigOption{
	{Label: "USDT · TRC20", Value: "USDT-TRC20"},
	{Label: "TRX · Tron", Value: "TRX"},
	{Label: "USDT · Polygon", Value: "USDT-Polygon"},
	{Label: "USDT · BSC", Value: "USDT-BSC"},
	{Label: "USDT · ERC20", Value: "USDT-ERC20"},
	{Label: "USDT · Arbitrum One", Value: "USDT-ArbitrumOne"},
	{Label: "USDC · ERC20", Value: "USDC-ERC20"},
	{Label: "USDC · Polygon", Value: "USDC-Polygon"},
	{Label: "USDC · BSC", Value: "USDC-BSC"},
	{Label: "USDC · Arbitrum One", Value: "USDC-ArbitrumOne"},
}

func ParseUpayConfig(raw json.RawMessage) (UpayConfig, error) {
	var c UpayConfig
	if json.Unmarshal(raw, &c) != nil {
		return c, fmt.Errorf("UPAY PRO 配置格式错误")
	}
	c.APIURL = strings.TrimRight(strings.TrimSpace(c.APIURL), "/")
	u, err := url.Parse(c.APIURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, fmt.Errorf("UPAY PRO 网关地址必须是完整 HTTP(S) 地址")
	}
	if strings.TrimSpace(c.SecretKey) == "" {
		return c, fmt.Errorf("UPAY PRO 通信密钥必填")
	}
	legacyDefault := c.TradeType == ""
	if legacyDefault {
		c.TradeType = "USDT-TRC20"
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if _, exists := fields["trade_types"]; !exists {
		c.TradeTypes = []string{c.TradeType}
	}
	if len(c.TradeTypes) == 0 {
		return c, fmt.Errorf("UPAY PRO 请至少选择一种币种与网络")
	}
	seen := make(map[string]bool)
	var trades []string
	for _, trade := range c.TradeTypes {
		if !slices.ContainsFunc(upayTrades, func(o port.ConfigOption) bool { return o.Value == trade }) {
			return c, fmt.Errorf("UPAY PRO 不支持该币种与网络组合")
		}
		if !seen[trade] {
			trades = append(trades, trade)
			seen[trade] = true
		}
	}
	c.TradeTypes = trades
	if legacyDefault {
		c.TradeType = trades[0]
	}
	if c.Timeout == 0 {
		c.Timeout = 1200
	}
	if c.Timeout < 60 || c.Timeout > 3600 {
		return c, fmt.Errorf("UPAY PRO 本地支付期限应为 60–3600 秒")
	}
	var extra map[string]json.RawMessage
	_ = json.Unmarshal(raw, &extra)
	for _, key := range []string{"currency", "target_currency", "fiat"} {
		if v, ok := extra[key]; ok {
			var s string
			if json.Unmarshal(v, &s) != nil || (s != "" && s != "CNY") {
				return c, fmt.Errorf("UPAY PRO 当前仅支持 CNY 计价")
			}
		}
	}
	return c, nil
}

// SelectTrade permits an omitted choice only for a single enabled network.
func (c UpayConfig) SelectTrade(method string) (string, error) {
	if method == "" && len(c.TradeTypes) == 1 {
		return c.TradeTypes[0], nil
	}
	if !slices.Contains(c.TradeTypes, method) {
		return "", fmt.Errorf("payment.METHOD_INVALID: 请选择该渠道已启用的币种与网络")
	}
	return method, nil
}

func (c UpayConfig) Options() []port.ConfigOption {
	var options []port.ConfigOption
	for _, trade := range c.TradeTypes {
		for _, option := range upayTrades {
			if option.Value == trade {
				options = append(options, option)
			}
		}
	}
	return options
}

func (c UpayConfig) Equal(other UpayConfig) bool {
	return c.APIURL == other.APIURL && c.SecretKey == other.SecretKey && c.TradeType == other.TradeType && c.Timeout == other.Timeout && slices.Equal(c.TradeTypes, other.TradeTypes)
}

type Upay struct{ client *http.Client }

func NewUpay() *Upay {
	return &Upay{client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (*Upay) Type() string                             { return "upay" }
func (*Upay) SuccessAck() string                       { return "success" }
func (*Upay) ValidateConfig(raw json.RawMessage) error { _, err := ParseUpayConfig(raw); return err }
func (*Upay) Meta() port.DriverMeta {
	return port.DriverMeta{Name: "UPAY PRO", Icon: "usdt", Description: "UPAY PRO 原生接口 · CNY 计价，支持 USDT、USDC 多链及 TRX"}
}
func (*Upay) ConfigFields() []port.ConfigField {
	return []port.ConfigField{
		{Key: "api_url", Label: "网关地址", Type: "text", Required: true, Placeholder: "https://pay.example.com", Help: "UPAY PRO 服务根地址，不含 /api/create_order"},
		{Key: "secret_key", Label: "通信密钥", Type: "password", Required: true, Sensitive: true, Help: "UPAY PRO 后台设置中的 SecretKey；留空保留已保存的密钥"},
		{Key: "trade_types", Label: "收款币种与网络", Type: "select", Multiple: true, Required: true, Default: "USDT-TRC20", Options: append([]port.ConfigOption(nil), upayTrades...), Help: "可多选；顾客选择币种与网络后进入对应收银台。请仅启用网关已配置钱包和汇率的选项。"},
		{Key: "timeout", Label: "本地支付期限（秒）", Type: "number", Default: "1200", Help: "60–3600 秒；同时受商品订单及网关有效期限制。网关期限需在 UPAY PRO 后台另行设置。"},
	}
}

// Upstream signs fixed fields with %g float formatting, including empty values.
// Unlike the prose in the API document, there is NO extra '&' before the key.
func upaySign(fields map[string]string, key string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+fields[k])
	}
	return md5Hex(strings.Join(parts, "&") + key)
}
func upayNumber(n json.Number) (float64, error) {
	// Reject exponent/overflow abuse before decimal parsing. Actual upstream JSON
	// amounts are bounded, positive, plain decimal numbers.
	if len(n.String()) == 0 || len(n.String()) > 32 {
		return 0, fmt.Errorf("invalid UPAY PRO amount")
	}
	for _, ch := range n.String() {
		if (ch < '0' || ch > '9') && ch != '.' {
			return 0, fmt.Errorf("invalid UPAY PRO amount")
		}
	}
	d, err := decimal.NewFromString(n.String())
	if err != nil || !d.IsPositive() || d.GreaterThan(decimal.NewFromInt(1<<53).Shift(-2)) {
		return 0, fmt.Errorf("invalid UPAY PRO amount")
	}
	f, err := n.Float64()
	if err != nil || !decimal.NewFromFloat(f).Equal(d) {
		return 0, fmt.Errorf("inexact UPAY PRO amount")
	}
	return f, nil
}
func upayCents(n json.Number) (money.Cents, error) {
	if _, err := upayNumber(n); err != nil {
		return 0, err
	}
	v, err := currencyunit.ParseChargeAmount(n.String(), currencyunit.ChargeUnit{Precision: 2, Step: 1})
	return money.Cents(v), err
}

func (p *Upay) CreatePayment(ctx context.Context, req port.CreatePaymentRequest) (*port.RedirectInfo, error) {
	c, err := ParseUpayConfig(req.Config)
	if err != nil {
		return nil, err
	}
	if req.GatewayOrderRef == "" || len(req.GatewayOrderRef) > 64 || req.Amount <= 0 || (req.ChargedCurrency != "" && req.ChargedCurrency != "CNY") || !time.Now().Before(req.Deadline) {
		return nil, fmt.Errorf("UPAY PRO 支付参数无效或订单已过期")
	}
	for _, value := range []string{req.NotifyBaseURL, req.ReturnURL} {
		u, e := url.Parse(value)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return nil, fmt.Errorf("UPAY PRO 回调与回跳地址必须为完整 HTTP(S) 地址")
		}
	}
	trade, err := c.SelectTrade(req.MethodCode)
	if err != nil {
		return nil, err
	}
	amount := json.Number(centsToYuan(int64(req.Amount)))
	f, err := upayNumber(amount)
	if err != nil {
		return nil, err
	}
	signature := upaySign(map[string]string{"type": trade, "amount": fmt.Sprintf("%g", f), "notify_url": req.NotifyBaseURL, "order_id": req.GatewayOrderRef, "redirect_url": req.ReturnURL}, c.SecretKey)
	body, _ := json.Marshal(map[string]any{"type": trade, "amount": amount, "order_id": req.GatewayOrderRef, "notify_url": req.NotifyBaseURL, "redirect_url": req.ReturnURL, "signature": signature})
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIURL+"/api/create_order", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("UPAY PRO 网关请求失败，结果待核对，请联系管理员；请勿重复付款")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("UPAY PRO 网关 HTTP %d，请检查网关配置并核对订单", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, fmt.Errorf("UPAY PRO 网关响应异常")
	}
	var result struct {
		StatusCode int `json:"status_code"`
		Data       struct {
			OrderID      string      `json:"order_id"`
			TradeID      string      `json:"trade_id"`
			Amount       json.Number `json:"amount"`
			ActualAmount json.Number `json:"actual_amount"`
			Token        string      `json:"token"`
			Expiration   int64       `json:"expiration_time"`
			PaymentURL   string      `json:"payment_url"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return nil, fmt.Errorf("UPAY PRO 网关响应格式异常")
	}
	if result.StatusCode != 200 {
		return nil, fmt.Errorf("UPAY PRO 拒绝下单（错误码 %d），请检查密钥、钱包和汇率", result.StatusCode)
	}
	d := result.Data
	got, err := upayCents(d.Amount)
	if err != nil || got != req.Amount || d.OrderID != req.GatewayOrderRef || d.TradeID == "" || len(d.TradeID) > 80 || d.Token == "" {
		return nil, fmt.Errorf("UPAY PRO 返回订单信息不一致")
	}
	if _, err := upayNumber(d.ActualAmount); err != nil {
		return nil, fmt.Errorf("UPAY PRO 返回实付金额无效")
	}
	u, err := url.Parse(d.PaymentURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, fmt.Errorf("UPAY PRO 收银台地址无效")
	}
	// Source uses UnixMilli; accept documented Unix seconds too.
	expires := time.UnixMilli(d.Expiration)
	if d.Expiration < 1e12 {
		expires = time.Unix(d.Expiration, 0)
	}
	if !time.Now().Before(expires) {
		return nil, fmt.Errorf("UPAY PRO 订单已过期")
	}
	deadline := req.Deadline
	if expires.Before(deadline) {
		deadline = expires
	}
	return &port.RedirectInfo{Type: "redirect", Payload: json.RawMessage(d.PaymentURL), ChannelOrderNo: d.TradeID, Deadline: deadline}, nil
}

func (*Upay) ParseWebhook(_ map[string]string, body []byte, cfg json.RawMessage) (*port.CallbackFact, error) {
	c, err := ParseUpayConfig(cfg)
	if err != nil {
		return nil, err
	}
	var n struct {
		TradeID      string      `json:"trade_id"`
		OrderID      string      `json:"order_id"`
		Amount       json.Number `json:"amount"`
		ActualAmount json.Number `json:"actual_amount"`
		Token        string      `json:"token"`
		BlockID      *string     `json:"block_transaction_id"`
		Status       int         `json:"status"`
		Signature    string      `json:"signature"`
	}
	if json.Unmarshal(body, &n) != nil || n.Signature == "" || n.OrderID == "" || len(n.OrderID) > 64 || n.TradeID == "" || len(n.TradeID) > 80 || n.Token == "" || n.BlockID == nil || n.Status < 1 || n.Status > 3 {
		return nil, fmt.Errorf("invalid UPAY PRO callback")
	}
	amount, err := upayCents(n.Amount)
	if err != nil {
		return nil, err
	}
	a, _ := n.Amount.Float64()
	actual, err := upayNumber(n.ActualAmount)
	if err != nil {
		return nil, err
	}
	expected := upaySign(map[string]string{"trade_id": n.TradeID, "order_id": n.OrderID, "amount": fmt.Sprintf("%g", a), "actual_amount": fmt.Sprintf("%g", actual), "token": n.Token, "block_transaction_id": *n.BlockID, "status": fmt.Sprint(n.Status)}, c.SecretKey)
	if !constantTimeEq(expected, n.Signature) {
		return nil, fmt.Errorf("invalid UPAY PRO signature")
	}
	// Credit original CNY amount only. actual_amount is crypto including increments.
	return &port.CallbackFact{Provider: "upay", GatewayOrderRef: n.OrderID, ChannelOrderNo: n.TradeID, Amount: amount, Currency: "CNY", Success: n.Status == 2, Raw: body}, nil
}
