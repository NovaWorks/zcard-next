package adapter

// Native BEpusdt protocol, verified against v1.24.2 (4d88040fd4096e77e8fb9ad2650e775753a977b6).
// This is independent of the GMPay adapter registered as epusdt.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/shopspring/decimal"
)

type BepusdtConfig struct {
	APIURL    string `json:"api_url"`
	APIToken  string `json:"api_token"`
	TradeType string `json:"trade_type"`
	Timeout   int64  `json:"timeout"`
}

func ParseBepusdtConfig(raw json.RawMessage) (BepusdtConfig, error) {
	var c BepusdtConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("BEpusdt 配置格式错误")
	}
	c.APIURL = strings.TrimRight(strings.TrimSpace(c.APIURL), "/")
	if c.TradeType == "" {
		c.TradeType = "usdt.trc20"
	}
	if c.Timeout == 0 {
		c.Timeout = 1200
	}
	u, err := url.Parse(c.APIURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, fmt.Errorf("BEpusdt 网关地址必须是完整 HTTP(S) 地址")
	}
	if strings.TrimSpace(c.APIToken) == "" {
		return c, fmt.Errorf("BEpusdt API Token 必填")
	}
	switch c.TradeType {
	case "usdt.trc20", "usdt.erc20", "usdt.bep20":
	default:
		return c, fmt.Errorf("BEpusdt 暂仅支持 USDT TRC20/ERC20/BEP20")
	}
	if c.Timeout < 180 || c.Timeout > 3600 {
		return c, fmt.Errorf("BEpusdt 支付期限应为 180–3600 秒")
	}
	var extra map[string]json.RawMessage
	_ = json.Unmarshal(raw, &extra)
	for _, key := range []string{"fiat", "currency", "target_currency"} {
		if v, ok := extra[key]; ok {
			var s string
			if json.Unmarshal(v, &s) != nil || (s != "" && s != "CNY") {
				return c, fmt.Errorf("BEpusdt 当前仅支持 CNY 计价")
			}
		}
	}
	return c, nil
}

type Bepusdt struct{ client *http.Client }

func NewBepusdt() *Bepusdt {
	return &Bepusdt{client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (*Bepusdt) Type() string       { return "bepusdt" }
func (*Bepusdt) SuccessAck() string { return "success" }
func (*Bepusdt) ValidateConfig(raw json.RawMessage) error {
	_, err := ParseBepusdtConfig(raw)
	return err
}
func (*Bepusdt) Meta() port.DriverMeta {
	return port.DriverMeta{Name: "BEpusdt", Icon: "usdt", Description: "BEpusdt 原生接口 · CNY 计价、USDT 收款（每个渠道固定一条链）"}
}
func (*Bepusdt) ConfigFields() []port.ConfigField {
	return []port.ConfigField{
		{Key: "api_url", Label: "网关地址", Type: "text", Required: true, Placeholder: "https://pay.example.com", Help: "BEpusdt 服务根地址，不含 /api/v1/order/create-transaction"},
		{Key: "api_token", Label: "API Token", Type: "password", Required: true, Sensitive: true, Help: "BEpusdt 后台 API 认证令牌"},
		{Key: "trade_type", Label: "收款网络", Type: "select", Required: true, Default: "usdt.trc20", Options: []port.ConfigOption{{Label: "USDT · TRC20", Value: "usdt.trc20"}, {Label: "USDT · ERC20", Value: "usdt.erc20"}, {Label: "USDT · BEP20", Value: "usdt.bep20"}}, Help: "网关需已配置对应钱包；支持新建多个渠道分别收款"},
		{Key: "timeout", Label: "支付期限（秒）", Type: "number", Default: "1200", Help: "180–3600 秒；同时受商品订单剩余期限限制。重试不会延长期限"},
	}
}

// Sign the JSON-decoded values exactly as upstream SignVerify does: numeric
// fields become float64 before fmt.Sprint, including exponent formatting. Only
// this protocol representation uses floats; settlement always uses exact cents.
func bepusdtSign(body []byte, token string) (string, error) {
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		return "", err
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if k != "signature" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		v := fields[k]
		if v == nil || v == "" {
			continue
		}
		switch v.(type) {
		case string, float64, bool:
		default:
			return "", fmt.Errorf("unsupported signature value")
		}
		pairs = append(pairs, k+"="+fmt.Sprint(v))
	}
	return md5Hex(strings.Join(pairs, "&") + token), nil
}

func bepusdtAmount(raw json.RawMessage) (money.Cents, error) {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("invalid amount")
	}
	f, err := n.Float64()
	if err != nil || f <= 0 || f > float64(1<<53)/100 || len(n.String()) > 64 {
		return 0, fmt.Errorf("invalid amount range")
	}
	d, err := decimal.NewFromString(n.String())
	if err != nil || !d.IsPositive() {
		return 0, fmt.Errorf("invalid amount")
	}
	cents := d.Shift(2)
	if !cents.Equal(cents.Truncate(0)) || cents.GreaterThan(decimal.NewFromInt(1<<53)) {
		return 0, fmt.Errorf("invalid amount precision or range")
	}
	return money.Cents(cents.IntPart()), nil
}

func (b *Bepusdt) CreatePayment(ctx context.Context, req port.CreatePaymentRequest) (*port.RedirectInfo, error) {
	c, err := ParseBepusdtConfig(req.Config)
	if err != nil {
		return nil, err
	}
	timeout := int64(math.Ceil(time.Until(req.Deadline).Seconds()))
	if timeout < 180 || timeout > 3600 {
		return nil, fmt.Errorf("payment.ORDER_EXPIRED: 剩余支付时间不足 180 秒，请重新下单")
	}
	if req.GatewayOrderRef == "" || req.Amount <= 0 || (req.ChargedCurrency != "" && req.ChargedCurrency != "CNY") {
		return nil, fmt.Errorf("invalid BEpusdt payment")
	}
	amount := json.Number(centsToYuan(int64(req.Amount)))
	// The upstream binder uses float64. Refuse values it cannot preserve.
	f, _ := amount.Float64()
	if !decimal.NewFromFloat(f).Equal(decimal.NewFromInt(int64(req.Amount)).Shift(-2)) {
		return nil, fmt.Errorf("BEpusdt amount exceeds exact protocol range")
	}
	fields := map[string]any{"order_id": req.GatewayOrderRef, "amount": amount, "fiat": "CNY", "trade_type": c.TradeType, "timeout": timeout, "notify_url": req.NotifyBaseURL, "redirect_url": req.ReturnURL, "name": req.Subject}
	body, _ := json.Marshal(fields)
	sig, err := bepusdtSign(body, c.APIToken)
	if err != nil {
		return nil, err
	}
	fields["signature"] = sig
	body, _ = json.Marshal(fields)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIURL+"/api/v1/order/create-transaction", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("BEpusdt 网关请求失败，请稍后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("BEpusdt 网关 HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var result struct {
		StatusCode int `json:"status_code"`
		Data       struct {
			OrderID    string          `json:"order_id"`
			TradeID    string          `json:"trade_id"`
			Fiat       string          `json:"fiat"`
			TradeType  string          `json:"trade_type"`
			Amount     json.RawMessage `json:"amount"`
			Status     int             `json:"status"`
			PaymentURL string          `json:"payment_url"`
			Expiration int64           `json:"expiration_time"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil || result.StatusCode != 200 {
		return nil, fmt.Errorf("BEpusdt 下单失败，请检查网关钱包、限额及回调地址配置")
	}
	d := result.Data
	got, err := bepusdtAmount(d.Amount)
	if err != nil || got != req.Amount || d.OrderID != req.GatewayOrderRef || d.Fiat != "CNY" || d.TradeType != c.TradeType || d.TradeID == "" || len(d.TradeID) > 80 {
		return nil, fmt.Errorf("BEpusdt 返回订单信息不一致")
	}
	if d.Status != 1 && d.Status != 2 && d.Status != 5 {
		return nil, fmt.Errorf("BEpusdt 订单不可支付")
	}
	u, err := url.Parse(d.PaymentURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return nil, fmt.Errorf("BEpusdt 收银台地址无效")
	}
	deadline := req.Deadline
	if d.Status == 1 {
		if d.Expiration <= 0 || d.Expiration > 3600 {
			return nil, fmt.Errorf("BEpusdt 订单已过期")
		}
		if upstream := time.Now().Add(time.Duration(d.Expiration) * time.Second); upstream.Before(deadline) {
			deadline = upstream
		}
	}
	return &port.RedirectInfo{Type: "redirect", Payload: json.RawMessage(d.PaymentURL), ChannelOrderNo: d.TradeID, Deadline: deadline}, nil
}

func (*Bepusdt) ParseWebhook(_ map[string]string, body []byte, cfg json.RawMessage) (*port.CallbackFact, error) {
	c, err := ParseBepusdtConfig(cfg)
	if err != nil {
		return nil, err
	}
	var n struct {
		Signature string          `json:"signature"`
		OrderID   string          `json:"order_id"`
		TradeID   string          `json:"trade_id"`
		Amount    json.RawMessage `json:"amount"`
		Status    int             `json:"status"`
	}
	if json.Unmarshal(body, &n) != nil || n.Signature == "" || n.OrderID == "" || n.TradeID == "" || len(n.OrderID) > 64 || len(n.TradeID) > 80 || n.Status < 1 || n.Status > 6 {
		return nil, fmt.Errorf("invalid BEpusdt callback")
	}
	sig, err := bepusdtSign(body, c.APIToken)
	if err != nil || !constantTimeEq(sig, n.Signature) {
		return nil, fmt.Errorf("invalid BEpusdt signature")
	}
	amount, err := bepusdtAmount(n.Amount)
	if err != nil {
		return nil, err
	}
	// amount is original CNY. actual_amount is crypto and MUST NOT credit CNY wallets.
	return &port.CallbackFact{Provider: "bepusdt", GatewayOrderRef: n.OrderID, ChannelOrderNo: n.TradeID, Amount: amount, Currency: "CNY", Success: n.Status == 2, Raw: body}, nil
}
