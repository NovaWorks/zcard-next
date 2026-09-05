package adapter

// paypal 适配器（）：Orders v2 手写 HTTP（PayPal 无官方 Go SDK——官方 SDK 仅
// Java/.NET/Node/PHP/Python/Ruby； 行为描述即手写规格，dujiao-next 主参照）。
//
// 能力位：Provider（Orders v2，intent=CAPTURE）+ Webhooker（官方 verify-webhook-signature
// API——5 头 + webhook_id）+ Capturer（先查后捕：GET order APPROVED→capture，COMPLETED→
// 直成功）+ Refunder（captures/{id}/refund——三参考项目均缺位，按 API 文档自建）。
//
// 协议要点（1.x PaypalDriver 对拍 + ）：
// - 金额：value 两位小数字符串（"10.00"，1.x bcdiv/dujiao Round(2) 同口径；零小数币种
// JPY 未适配——现阶段目标币种仅 precision=2，联调锁定）
// - 幂等：reference_id/invoice_id 双写 OrderNo（1.x 用 reference_id，钉 invoice_id）
// - return 同步捕获：PayPal 跳回 return_url 时追加 token=<order_id>（1.x 生产依赖）；
// return 端点复用 Capturer 先查后捕
// - 验签：POST /v1/notifications/verify-webhook-signature，请求体 = 元数据 JSON 拼接
// 原始事件字节（签名覆盖原文——字节必须原样透传，禁止重排/重序列化）
// - token 缓存：包级并发安全缓存（key=base_url|client_id），expires_in 提前 5 分钟失效
// （1.x M-11：匿名回调端点触发 token 换发可被打满商户 API 速率配额）
// - 退款锚点：支付单 channel_order_no 存 PayPal order id（Capturer/return 同锚点），
// Refund 内先 GET order 解析 capture id（退款锚定实收捕获）
//
// 安全纪律：匿名端点（return/webhook）出站 token 白名单约束（1.x M-11 速率配额放大器）；
// 验签失败 401 与单号错误 400 语义分离（回调管线契约）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// paypalConfig 渠道凭据（解密后 JSON；admin 支付渠道配置）。
type paypalConfig struct {
	ClientID       string `json:"client_id"`
	ClientSecret   string `json:"client_secret"`
	Mode           string `json:"mode"` // sandbox / live（base_url 缺省依据）
	BaseURL        string `json:"base_url"`
	WebhookID      string `json:"webhook_id"`
	BrandName      string `json:"brand_name"`
	TargetCurrency string `json:"target_currency"` // 跨币目标（空=CNY 直收）
}

// paypalBaseURL 渠道 API 根（base_url 覆盖优先；否则按 mode 取官方端点）。
func paypalBaseURL(c paypalConfig) string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	if c.Mode == "sandbox" {
		return "https://api-m.sandbox.paypal.com"
	}
	return "https://api-m.paypal.com"
}

// PaypalAdapter Orders v2 适配器（无状态——凭据逐调用传入；token 缓存包级共享）。
type PaypalAdapter struct{}

// NewPaypal 构造。
func NewPaypal() *PaypalAdapter { return &PaypalAdapter{} }

// Type 渠道驱动名。
func (a *PaypalAdapter) Type() string { return "paypal" }

// ValidateConfig 校验凭据必填。
func (a *PaypalAdapter) ValidateConfig(cfg json.RawMessage) error {
	var c paypalConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return fmt.Errorf("paypal: 凭据格式错误: %w", err)
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		return fmt.Errorf("paypal: client_id/client_secret 必填")
	}
	if c.BaseURL == "" && c.Mode != "sandbox" && c.Mode != "live" {
		return fmt.Errorf("paypal: mode 须为 sandbox/live（或配置 base_url）")
	}
	return nil
}

// ── OAuth2 token（client_credentials + 包级缓存，提前 5 分钟失效）──

type paypalTokenEntry struct {
	token   string
	expires time.Time
}

var paypalTokenCache sync.Map // key: base_url|client_id → *paypalTokenEntry

// paypalAccessToken 取 token：缓存命中（未过期）直返；否则换发并缓存。
// 1.x M-11：匿名回调/return 端点每请求换 token 会打满商户速率配额——缓存必须。
func paypalAccessToken(ctx context.Context, c paypalConfig) (string, error) {
	base := paypalBaseURL(c)
	key := base + "|" + c.ClientID
	if v, ok := paypalTokenCache.Load(key); ok {
		if e := v.(*paypalTokenEntry); time.Now().Before(e.expires) {
			return e.token, nil
		}
	}
	token, expiresIn, err := paypalFetchToken(ctx, base, c)
	if err != nil {
		return "", err
	}
	// 提前 5 分钟失效（边界竞态防护；expires_in<=300 不缓存——短寿 token 无缓存价值）
	if expiresIn > 300 {
		paypalTokenCache.Store(key, &paypalTokenEntry{
			token:   token,
			expires: time.Now().Add(time.Duration(expiresIn-300) * time.Second),
		})
	}
	return token, nil
}

// paypalFetchToken POST /v1/oauth2/token（Basic 认证 + 表单）。
func paypalFetchToken(ctx context.Context, base string, c paypalConfig) (string, int64, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("paypal: 构造 token 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.ClientID, c.ClientSecret)
	resp, err := paypalHTTPClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("paypal: 换发 token 失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("paypal: 换发 token HTTP %d", resp.StatusCode)
	}
	var reply struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil || reply.AccessToken == "" {
		return "", 0, fmt.Errorf("paypal: token 响应解析失败")
	}
	return reply.AccessToken, reply.ExpiresIn, nil
}

// paypalHTTPClient 出站客户端（15s 超时——匿名端点出站请求受时限约束）。
var paypalHTTPClient = &http.Client{Timeout: 15 * time.Second}

// paypalDo JSON 请求（Bearer 认证）。
func paypalDo(ctx context.Context, base, method, path, token string, body []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("paypal: 构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := paypalHTTPClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("paypal: 请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return raw, resp.StatusCode, nil
}

// ── Orders v2 数据结构（仅取用字段；未知字段忽略——协议演进安全）──

type paypalMoney struct {
	CurrencyCode string `json:"currency_code"`
	Value        string `json:"value"`
}

type paypalCapture struct {
	ID     string      `json:"id"`
	Status string      `json:"status"`
	Amount paypalMoney `json:"amount"`
}

type paypalPurchaseUnit struct {
	ReferenceID string      `json:"reference_id"`
	InvoiceID   string      `json:"invoice_id"`
	Description string      `json:"description"`
	Amount      paypalMoney `json:"amount"`
	Payments    struct {
		Captures []paypalCapture `json:"captures"`
	} `json:"payments"`
}

// paypalOrder 订单资源（GET order / capture 响应 / 订单类 webhook 事件共用）。
type paypalOrder struct {
	ID            string               `json:"id"`
	Status        string               `json:"status"`
	PurchaseUnits []paypalPurchaseUnit `json:"purchase_units"`
}

// paypalOrderIDRe 单号白名单（1.x M-11：匿名端点出站放大器——仅字母数字大写 5-30 位）。
var paypalOrderIDRe = regexp.MustCompile(`^[A-Z0-9]{5,30}$`)

// ── Provider：下单 ──

// CreatePayment Orders v2（intent=CAPTURE，redirect 到 PayPal 授权页）。
func (a *PaypalAdapter) CreatePayment(ctx context.Context, req port.CreatePaymentRequest) (*port.RedirectInfo, error) {
	var c paypalConfig
	if err := json.Unmarshal(req.Config, &c); err != nil {
		return nil, fmt.Errorf("paypal: 凭据格式错误: %w", err)
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		return nil, fmt.Errorf("paypal: client_id/client_secret 必填")
	}
	// 金额口径（ 快照）：跨币用 ChargedUnits/ChargedCurrency；同币直收回落 Amount
	units := int64(req.Amount)
	currency := "CNY"
	if req.ChargedUnits > 0 {
		units = req.ChargedUnits
		currency = strings.ToUpper(req.ChargedCurrency)
	} else if c.TargetCurrency != "" {
		// 快照缺席（同币直收）但渠道声明跨币目标——配置矛盾，拒绝（fail-closed）
		return nil, fmt.Errorf("paypal: 快照缺失（currency 表未配置 %s？）", c.TargetCurrency)
	}
	returnURL := req.ReturnURL
	if returnURL == "" {
		// return 同步捕获端点（PayPal 跳回时追加 token=<order_id>）
		returnURL = "/payments/return/" + req.Channel
	}
	token, err := paypalAccessToken(ctx, c)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"intent": "CAPTURE",
		"purchase_units": []any{map[string]any{
			"reference_id": req.OrderNo, // 1.x 定位口径（捕获响应回读）
			"invoice_id":   req.OrderNo, // 幂等（ ）
			"description":  req.Subject,
			"amount": paypalMoney{
				CurrencyCode: currency,
				Value:        centsToYuan(units), // 两位小数字符串（ 钉死）
			},
		}},
		"application_context": paypalAppContext(c, returnURL),
	}
	body, _ := json.Marshal(payload)
	raw, status, err := paypalDo(ctx, paypalBaseURL(c), http.MethodPost, "/v2/checkout/orders", token, body)
	if err != nil {
		return nil, fmt.Errorf("paypal: 下单失败: %w", err)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("paypal: 下单被网关拒绝 HTTP %d: %s", status, paypalErrDetail(raw))
	}
	var order paypalOrder
	if err := json.Unmarshal(raw, &order); err != nil || order.ID == "" {
		return nil, fmt.Errorf("paypal: 下单响应解析失败")
	}
	// approve 链接优先；缺失时按 1.x 兜底拼授权页（token=<order_id>）
	approve := ""
	for _, l := range paypalLinks(raw) {
		if strings.EqualFold(l["rel"], "approve") {
			approve = l["href"]
		}
	}
	if approve == "" {
		host := "https://www.paypal.com"
		if c.Mode == "sandbox" {
			host = "https://www.sandbox.paypal.com"
		}
		approve = host + "/checkoutnow?token=" + order.ID
	}
	payload2, _ := json.Marshal(map[string]string{"url": approve})
	return &port.RedirectInfo{Type: "redirect", Payload: payload2}, nil
}

// paypalAppContext 授权上下文（brand_name 可选——空值不发送）。
func paypalAppContext(c paypalConfig, returnURL string) map[string]string {
	ac := map[string]string{
		"return_url":          returnURL,
		"cancel_url":          returnURL + "?status=cancel",
		"user_action":         "PAY_NOW",
		"shipping_preference": "NO_SHIPPING",
	}
	if c.BrandName != "" {
		ac["brand_name"] = c.BrandName
	}
	return ac
}

// paypalLinks 提取响应 links 数组（rel→href）。
func paypalLinks(raw []byte) []map[string]string {
	var resp struct {
		Links []map[string]string `json:"links"`
	}
	if json.Unmarshal(raw, &resp) != nil {
		return nil
	}
	return resp.Links
}

// paypalErrDetail 网关错误详情（截断入日志/错误；凭据绝不入错误信息）。
func paypalErrDetail(raw []byte) string {
	var resp struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &resp) == nil && resp.Message != "" {
		if len(resp.Message) > 200 {
			return resp.Message[:200]
		}
		return resp.Message
	}
	return "unknown"
}

// ── Webhooker：官方验签 API + 事件映射 ──

// paypalWebhookVerifyMetadata 验签请求元数据（snake_case 字段——PayPal 文档口径）。
type paypalWebhookVerifyMetadata struct {
	AuthAlgo         string `json:"auth_algo"`
	CertURL          string `json:"cert_url"`
	TransmissionID   string `json:"transmission_id"`
	TransmissionSig  string `json:"transmission_sig"`
	TransmissionTime string `json:"transmission_time"`
	WebhookID        string `json:"webhook_id"`
}

// paypalVerifyBody 验签请求体 = 元数据 JSON 拼接原始事件字节（签名覆盖原文——
// 必须原样透传，重排/重序列化将破坏签名；dujiao marshalWebhookVerifyRequest 同法）。
func paypalVerifyBody(meta paypalWebhookVerifyMetadata, event []byte) []byte {
	head, _ := json.Marshal(meta)
	body := make([]byte, 0, len(head)+len(event)+len(`,"webhook_event":`)+1)
	body = append(body, head[:len(head)-1]...)
	body = append(body, `,"webhook_event":`...)
	body = append(body, event...)
	body = append(body, '}')
	return body
}

// paypalHeader 大小写不敏感取头（回调路由透传 http.Header——Go 规范化键）。
func paypalHeader(headers map[string]string, name string) string {
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// ParseWebhook 官方验签 API（5 头 + webhook_id）→ 事件映射 → CallbackFact。
func (a *PaypalAdapter) ParseWebhook(headers map[string]string, body []byte, cfg json.RawMessage) (*port.CallbackFact, error) {
	var c paypalConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return nil, fmt.Errorf("paypal: 凭据格式错误: %w", err)
	}
	if c.WebhookID == "" {
		return nil, fmt.Errorf("paypal: webhook_id 未配置")
	}
	meta := paypalWebhookVerifyMetadata{
		AuthAlgo:         paypalHeader(headers, "Paypal-Auth-Algo"),
		CertURL:          paypalHeader(headers, "Paypal-Cert-Url"),
		TransmissionID:   paypalHeader(headers, "Paypal-Transmission-Id"),
		TransmissionSig:  paypalHeader(headers, "Paypal-Transmission-Sig"),
		TransmissionTime: paypalHeader(headers, "Paypal-Transmission-Time"),
		WebhookID:        c.WebhookID,
	}
	for name, v := range map[string]string{
		"auth_algo": meta.AuthAlgo, "cert_url": meta.CertURL,
		"transmission_id": meta.TransmissionID, "transmission_sig": meta.TransmissionSig,
		"transmission_time": meta.TransmissionTime,
	} {
		if v == "" {
			return nil, fmt.Errorf("paypal: 缺 %s 头", name)
		}
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("paypal: 事件体非 JSON")
	}
	ctx := context.Background()
	token, err := paypalAccessToken(ctx, c)
	if err != nil {
		return nil, err
	}
	raw, status, err := paypalDo(ctx, paypalBaseURL(c), http.MethodPost,
		"/v1/notifications/verify-webhook-signature", token, paypalVerifyBody(meta, body))
	if err != nil {
		return nil, fmt.Errorf("paypal: 验签请求失败: %w", err)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("paypal: 验签 API HTTP %d", status)
	}
	var vr struct {
		VerificationStatus string `json:"verification_status"`
	}
	if err := json.Unmarshal(raw, &vr); err != nil {
		return nil, fmt.Errorf("paypal: 验签响应解析失败")
	}
	if !strings.EqualFold(vr.VerificationStatus, "SUCCESS") {
		return nil, fmt.Errorf("paypal: 验签失败（verification_status=%s）", vr.VerificationStatus)
	}
	return a.factFromEvent(ctx, c, body)
}

// paypalWebhookEvent 事件信封（resource 原样保留——按事件类型二次解析）。
type paypalWebhookEvent struct {
	ID         string          `json:"id"`
	EventType  string          `json:"event_type"`
	CreateTime string          `json:"create_time"`
	Resource   json.RawMessage `json:"resource"`
}

// factFromEvent 事件 → CallbackFact。成功事件：
// - PAYMENT.CAPTURE.COMPLETED：capture 资源无 invoice_id——经 related_ids.order_id
// 出站拉订单解析（resolve 失败 → 错误 → 网关重试）
// - CHECKOUT.ORDER.COMPLETED：订单资源自带 reference_id/invoice_id，零出站
//
// 其余状态事件（pending/denied/declined/failed…）Success=false——管线忽略，
// 仅成功事件推进支付。
func (a *PaypalAdapter) factFromEvent(ctx context.Context, c paypalConfig, body []byte) (*port.CallbackFact, error) {
	var ev paypalWebhookEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, fmt.Errorf("paypal: 事件解析失败: %w", err)
	}
	et := strings.ToUpper(ev.EventType)
	switch et {
	case "PAYMENT.CAPTURE.COMPLETED":
		var cap struct {
			ID                string      `json:"id"`
			Status            string      `json:"status"`
			Amount            paypalMoney `json:"amount"`
			SupplementaryData struct {
				RelatedIDs struct {
					OrderID string `json:"order_id"`
				} `json:"related_ids"`
			} `json:"supplementary_data"`
		}
		if err := json.Unmarshal(ev.Resource, &cap); err != nil {
			return nil, fmt.Errorf("paypal: capture 事件解析失败: %w", err)
		}
		if !paypalOrderIDRe.MatchString(cap.SupplementaryData.RelatedIDs.OrderID) {
			return nil, fmt.Errorf("paypal: capture 事件缺 related order_id")
		}
		order, err := paypalGetOrder(ctx, c, cap.SupplementaryData.RelatedIDs.OrderID)
		if err != nil {
			return nil, err
		}
		fact := a.factFromOrder(*order, true)
		fact.Raw = body
		return fact, nil
	case "CHECKOUT.ORDER.COMPLETED":
		var order paypalOrder
		if err := json.Unmarshal(ev.Resource, &order); err != nil {
			return nil, fmt.Errorf("paypal: order 事件解析失败: %w", err)
		}
		fact := a.factFromOrder(order, true)
		fact.Raw = body
		return fact, nil
	case "CHECKOUT.ORDER.APPROVED", "PAYMENT.CAPTURE.PENDING",
		"PAYMENT.CAPTURE.DENIED", "PAYMENT.CAPTURE.DECLINED",
		"PAYMENT.CAPTURE.FAILED", "PAYMENT.CAPTURE.REVERSED",
		"PAYMENT.CAPTURE.REFUNDED", "CHECKOUT.ORDER.DENIED",
		"CHECKOUT.ORDER.CANCELED", "CHECKOUT.ORDER.PENDING":
		// pending/失败事件：Success=false——管线忽略（仅成功推进）
		return &port.CallbackFact{Provider: "paypal", Success: false, Raw: body}, nil
	default:
		// 未知事件（订阅范围外）——按 resource.status 兜底：COMPLETED 才推进
		var order paypalOrder
		if err := json.Unmarshal(ev.Resource, &order); err == nil && order.Status == "COMPLETED" {
			fact := a.factFromOrder(order, true)
			fact.Raw = body
			return fact, nil
		}
		return &port.CallbackFact{Provider: "paypal", Success: false, Raw: body}, nil
	}
}

// ── Capturer：先查后捕（return 同步捕获 / admin 补单共用）──

// paypalGetOrder GET /v2/checkout/orders/{id}。
func paypalGetOrder(ctx context.Context, c paypalConfig, orderID string) (*paypalOrder, error) {
	token, err := paypalAccessToken(ctx, c)
	if err != nil {
		return nil, err
	}
	path := "/v2/checkout/orders/" + url.PathEscape(orderID)
	raw, status, err := paypalDo(ctx, paypalBaseURL(c), http.MethodGet, path, token, nil)
	if err != nil {
		return nil, fmt.Errorf("paypal: 查单失败: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("paypal: 查单 HTTP %d", status)
	}
	var order paypalOrder
	if err := json.Unmarshal(raw, &order); err != nil || order.ID == "" {
		return nil, fmt.Errorf("paypal: 查单响应解析失败")
	}
	return &order, nil
}

// QueryPayment 先查后捕：GET order → APPROVED 才 capture（ 幂等——
// 重复 capture 报错，先查避免）；COMPLETED 直成功（已捕获，幂等短路）；其余未付。
func (a *PaypalAdapter) QueryPayment(ctx context.Context, gatewayOrderNo string, cfg json.RawMessage) (*port.CallbackFact, error) {
	var c paypalConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return nil, fmt.Errorf("paypal: 凭据格式错误: %w", err)
	}
	if !paypalOrderIDRe.MatchString(gatewayOrderNo) {
		return nil, fmt.Errorf("paypal: 单号格式非法")
	}
	order, err := paypalGetOrder(ctx, c, gatewayOrderNo)
	if err != nil {
		return nil, err
	}
	switch order.Status {
	case "COMPLETED":
		return a.factFromOrder(*order, true), nil
	case "APPROVED":
		token, err := paypalAccessToken(ctx, c)
		if err != nil {
			return nil, err
		}
		raw, status, err := paypalDo(ctx, paypalBaseURL(c), http.MethodPost,
			"/v2/checkout/orders/"+url.PathEscape(gatewayOrderNo)+"/capture", token, []byte("{}"))
		if err != nil {
			return nil, fmt.Errorf("paypal: 捕获失败: %w", err)
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("paypal: 捕获被网关拒绝 HTTP %d: %s", status, paypalErrDetail(raw))
		}
		var captured paypalOrder
		if err := json.Unmarshal(raw, &captured); err != nil {
			return nil, fmt.Errorf("paypal: 捕获响应解析失败: %w", err)
		}
		return a.factFromOrder(captured, true), nil
	default:
		// CREATED/PAYER_ACTION_REQUIRED/SAVED/VOIDED…：未支付——Success=false
		//（OrderNo 仍需携带——return 端点 fallback 跳转依赖）
		return a.factFromOrder(*order, false), nil
	}
}

// factFromOrder 订单资源 → CallbackFact（成功判定：首个捕获 COMPLETED）。
// OrderNo = reference_id 优先（1.x 口径），invoice_id 兜底。
func (a *PaypalAdapter) factFromOrder(o paypalOrder, success bool) *port.CallbackFact {
	fact := &port.CallbackFact{Provider: "paypal", ChannelOrderNo: o.ID, Success: false}
	if len(o.PurchaseUnits) == 0 {
		return fact
	}
	u := o.PurchaseUnits[0]
	fact.OrderNo = u.ReferenceID
	if fact.OrderNo == "" {
		fact.OrderNo = u.InvoiceID
	}
	// 金额取实收捕获（回显下单金额—— 快照核对口径）；捕获缺席回落订单金额
	if len(u.Payments.Captures) > 0 {
		cap := u.Payments.Captures[0]
		if cents, err := yuanToCents(cap.Amount.Value); err == nil {
			fact.Amount = money.Cents(cents)
		}
		fact.Currency = strings.ToUpper(cap.Amount.CurrencyCode)
		fact.Success = success && cap.Status == "COMPLETED"
	} else if u.Amount.Value != "" {
		if cents, err := yuanToCents(u.Amount.Value); err == nil {
			fact.Amount = money.Cents(cents)
		}
		fact.Currency = strings.ToUpper(u.Amount.CurrencyCode)
	}
	return fact
}

// ── Refunder：captures/{id}/refund ──

// Refund 原路退款。gatewayOrderNo 语义 = PayPal order id（支付单 channel_order_no
// 落库口径）——先 GET order 解析 capture id（实收捕获锚点）；amount 语义 = 渠道币种
// 最小单位（charged_units 快照口径——调用方从支付单快照取数，零二次换算）；
// amount<=0 全额退（请求体空对象）。
func (a *PaypalAdapter) Refund(ctx context.Context, gatewayOrderNo string, amount money.Cents, reason string, cfg json.RawMessage) error {
	var c paypalConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return fmt.Errorf("paypal: 凭据格式错误: %w", err)
	}
	if !paypalOrderIDRe.MatchString(gatewayOrderNo) {
		return fmt.Errorf("paypal: 单号格式非法")
	}
	order, err := paypalGetOrder(ctx, c, gatewayOrderNo)
	if err != nil {
		return err
	}
	if len(order.PurchaseUnits) == 0 || len(order.PurchaseUnits[0].Payments.Captures) == 0 {
		return fmt.Errorf("paypal: 无可退捕获")
	}
	cap := order.PurchaseUnits[0].Payments.Captures[0]
	body := []byte("{}")
	if int64(amount) > 0 {
		b, _ := json.Marshal(map[string]any{"amount": paypalMoney{
			CurrencyCode: cap.Amount.CurrencyCode,
			Value:        centsToYuan(int64(amount)),
		}})
		body = b
	}
	token, err := paypalAccessToken(ctx, c)
	if err != nil {
		return err
	}
	raw, status, err := paypalDo(ctx, paypalBaseURL(c), http.MethodPost,
		"/v2/payments/captures/"+url.PathEscape(cap.ID)+"/refund", token, body)
	if err != nil {
		return fmt.Errorf("paypal: 退款失败: %w", err)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("paypal: 退款被网关拒绝 HTTP %d: %s", status, paypalErrDetail(raw))
	}
	return nil
}
