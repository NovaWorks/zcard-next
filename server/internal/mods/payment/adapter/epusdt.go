package adapter

// epusdt 适配器（）：GMPay 协议（github.com/GMWalletApp/epusdt 自托管 USDT 网关）。
//
// 协议（1.x EpuSdtDriver 沉淀 + dujiao-next gateway/epusdt 对拍）：
// - 下单：POST {api_url}/payments/gmpay/v1/order/create-transaction
// 参数 pid/order_id/currency/amount(法币元两位小数)/notify_url/redirect_url/name/network/token
// - 签名：剔 signature + 空值 → key ASCII 字典序 → k=v& 拼接 → HMAC-SHA256(secret) 小写 hex
// - 回调：JSON POST；status==2 支付成功；重签对比；amount 为法币元（与下单同口径，零换算）
// - 应答：纯文本 "ok"（Acker 能力位；管线 JSON 兜底不适用于本协议）
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
	// 收款方式（ 多选）：单值字段兼容旧配置；多选数组优先。
	// 恰好一币一链 → 下单锁定该方式；多选/未选 → 占位订单（不传 token/network，
	// 顾客在 epusdt 收银台从服务端启用的链/币中自选——官方协议支持）
	Token    string   `json:"token"`
	Network  string   `json:"network"`
	Tokens   []string `json:"tokens,omitempty"`
	Networks []string `json:"networks,omitempty"`
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
	// 币种快照（）：跨币路径服务端已换算——ChargedUnits/ChargedCurrency
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
	params := map[string]string{
		"pid":      c.PID,
		"order_id": req.OrderNo,
		"currency": currency,
		// GMPay cny/usd 均两位小数（最小单位=分/美分同构）；快照口径直接出口
		"amount":       centsToYuan(chargeUnits),
		"notify_url":   req.NotifyBaseURL,
		"redirect_url": req.ReturnURL,
		"name":         req.Subject,
	}
	// 收款方式锁定优先级：① 收银台顾客所选方式（method.params.network/token——按链选择）
	// ② 渠道凭据恰好一币一链 → 锁定；③ 多选/未选 → 不传（GMPay 占位订单，官方收银台自选）
	if nw := req.MethodParams["network"]; nw != "" {
		params["network"] = strings.ToLower(nw)
		if tk := req.MethodParams["token"]; tk != "" {
			params["token"] = tk
		}
	} else if tks, nws := epusdtTokens(c), epusdtNetworks(c); len(tks) == 1 && len(nws) == 1 {
		params["token"] = tks[0]
		params["network"] = strings.ToLower(nws[0]) // 协议 network 小写（tron/erc20…）
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

// ── 字段选项动态加载（ 修复：network/token 以网关 supported_assets 为准）──

// epusdtHTTPClient 出站客户端（配置面动态选项拉取）。
var epusdtHTTPClient = &http.Client{Timeout: 10 * time.Second}

// epusdtStaticNetworkOptions 静态回落网络矩阵（README 支持表；value 用协议小写值）。
func epusdtStaticNetworkOptions() []port.ConfigOption {
	return []port.ConfigOption{
		{Label: "TRC20（波场 Tron）", Value: "tron"},
		{Label: "ERC20（以太坊 Ethereum）", Value: "erc20"},
		{Label: "BEP20（BNB Chain）", Value: "bep20"},
		{Label: "Solana", Value: "solana"},
		{Label: "Polygon", Value: "polygon"},
		{Label: "Aptos", Value: "aptos"},
	}
}

// epusdtStaticTokenOptions 静态回落代币矩阵（README 支持表并集）。
func epusdtStaticTokenOptions() []port.ConfigOption {
	return []port.ConfigOption{
		{Label: "USDT", Value: "USDT"},
		{Label: "USDC", Value: "USDC"},
		{Label: "TRX", Value: "TRX"},
		{Label: "ETH", Value: "ETH"},
		{Label: "BNB", Value: "BNB"},
	}
}

// epusdtConfigReply GET /payments/gmpay/v1/config 响应（data.supported_assets——
// 服务端启用的链与代币，官方文档明示为动态来源）。
type epusdtConfigReply struct {
	Data struct {
		SupportedAssets []struct {
			Network     string   `json:"network"`
			DisplayName string   `json:"display_name"`
			Tokens      []string `json:"tokens"`
		} `json:"supported_assets"`
	} `json:"data"`
}

// FieldOptions network/token 动态选项：网关 supported_assets 为准；
// api_url 缺失或网关不可达 → 静态矩阵回落（fail-safe，表单始终可用）。
// token 级联：partial 带 network 时仅返回该链代币（前端联动刷新）。
func (a *EpusdtAdapter) FieldOptions(ctx context.Context, field string, partial json.RawMessage) (port.FieldOptionsResult, error) {
	var probe struct {
		APIURL string `json:"api_url"`
	}
	_ = json.Unmarshal(partial, &probe)
	if strings.TrimSpace(probe.APIURL) == "" {
		return port.FieldOptionsResult{Options: a.fieldOptionsStatic(field, partial)}, nil
	}
	assets, err := epusdtFetchAssets(ctx, probe.APIURL)
	if err != nil {
		// 网关不可达/未配好：静态回落（fallback 标记供前端提示）
		return port.FieldOptionsResult{Options: a.fieldOptionsStatic(field, partial), Fallback: true}, nil
	}
	return port.FieldOptionsResult{Options: a.fieldOptionsFromAssets(field, assets, partial)}, nil
}

func (a *EpusdtAdapter) fieldOptionsStatic(field string, partial json.RawMessage) []port.ConfigOption {
	switch field {
	case "network":
		return epusdtStaticNetworkOptions()
	case "token":
		return epusdtStaticTokenOptions()
	}
	return nil
}

func (a *EpusdtAdapter) fieldOptionsFromAssets(field string, assets []epusdtSupportedAsset, partial json.RawMessage) []port.ConfigOption {
	switch field {
	case "network":
		out := make([]port.ConfigOption, 0, len(assets))
		for _, as := range assets {
			if strings.TrimSpace(as.Network) == "" {
				continue
			}
			label := strings.TrimSpace(as.DisplayName)
			if label == "" {
				label = as.Network
			}
			out = append(out, port.ConfigOption{Label: label, Value: as.Network})
		}
		return out
	case "token":
		// 级联：network 多选 → 已选链的代币并集（前端联动刷新）
		want := epusdtPartialNetworks(partial)
		seen := map[string]bool{}
		out := make([]port.ConfigOption, 0)
		for _, as := range assets {
			if len(want) > 0 && !want[strings.ToLower(as.Network)] {
				continue
			}
			for _, tk := range as.Tokens {
				t := strings.ToUpper(strings.TrimSpace(tk))
				if t == "" || seen[t] {
					continue
				}
				seen[t] = true
				out = append(out, port.ConfigOption{Label: t, Value: t})
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
		return out
	}
	return nil
}

// epusdtSupportedAsset supported_assets 条目。
type epusdtSupportedAsset struct {
	Network     string
	DisplayName string
	Tokens      []string
}

// epusdtTokens/epusdtNetworks 收款方式取值（多选数组优先，旧单值兜底）。
func epusdtTokens(c epusdtConfig) []string {
	if len(c.Tokens) > 0 {
		return c.Tokens
	}
	if c.Token != "" {
		return []string{c.Token}
	}
	return nil
}

func epusdtNetworks(c epusdtConfig) []string {
	if len(c.Networks) > 0 {
		return c.Networks
	}
	if c.Network != "" {
		return []string{c.Network}
	}
	return nil
}

// epusdtPartialNetworks 解析级联过滤的 network 集合（字符串/逗号分隔/数组兼容）。
func epusdtPartialNetworks(partial json.RawMessage) map[string]bool {
	var probe struct {
		Network json.RawMessage `json:"network"`
	}
	if json.Unmarshal(partial, &probe) != nil || len(probe.Network) == 0 {
		return nil
	}
	out := map[string]bool{}
	var one string
	if json.Unmarshal(probe.Network, &one) == nil {
		for _, n := range strings.Split(one, ",") {
			if n = strings.TrimSpace(n); n != "" {
				out[strings.ToLower(n)] = true
			}
		}
		return out
	}
	var many []string
	if json.Unmarshal(probe.Network, &many) == nil {
		for _, n := range many {
			if n = strings.TrimSpace(n); n != "" {
				out[strings.ToLower(n)] = true
			}
		}
	}
	return out
}

// epusdtFetchAssets 拉取网关支持矩阵（超时 10s；失败返回 error → 调用方回落）。
func epusdtFetchAssets(ctx context.Context, apiURL string) ([]epusdtSupportedAsset, error) {
	url := strings.TrimRight(apiURL, "/") + "/payments/gmpay/v1/config"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := epusdtHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("config HTTP %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var reply epusdtConfigReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, err
	}
	out := make([]epusdtSupportedAsset, 0, len(reply.Data.SupportedAssets))
	for _, as := range reply.Data.SupportedAssets {
		out = append(out, epusdtSupportedAsset{Network: as.Network, DisplayName: as.DisplayName, Tokens: as.Tokens})
	}
	return out, nil
}
