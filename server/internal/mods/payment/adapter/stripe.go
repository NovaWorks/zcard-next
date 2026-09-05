package adapter

// stripe 适配器（）：官方 stripe-go SDK（用户决策——验签/tolerance/重试由 SDK 承担）。
//
// 能力位：Provider（Checkout Session）+ Webhooker（Stripe-Signature 构造验证）
// + Capturer（session 查单补单）+ Refunder（Refunds API——三参考项目均缺位，本仓自建）。
//
// 协议要点（dujiao/1.x 对拍 + 快照）：
// - 下单：mode=payment Checkout Session；跨币金额走 ChargedUnits/ChargedCurrency
// （ 服务端快照口径），同币回落 Amount；metadata[order_no] 与
// client_reference_id 双写（回调双定位）
// - 回调：checkout.session.completed / async_payment_succeeded → 成功；
// expired / async_payment_failed → 失败（Success=false 管线忽略）
// - 查单：cs_ 前缀查 session（expand payment_intent）
// - 退款：Refunder amount 语义 = 渠道币种最小单位（charged_units 快照口径——
// 跨境退款按实收原币退，杜绝二次换算；调用方从支付单快照取数）
//
// SDK 使用纪律：client.New(key, backends) 每调用构造（无全局 stripe.Key 突变——
// 适配器无状态并发安全）；测试注入 httptest backend（NewBackendsWithConfig.URL）。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stripe/stripe-go/v82"
	stripec "github.com/stripe/stripe-go/v82/client"
	"github.com/stripe/stripe-go/v82/webhook"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// stripeConfig 渠道凭据（解密后 JSON）。
type stripeConfig struct {
	SecretKey      string `json:"secret_key"`      // sk_live_/sk_test_
	WebhookSecret  string `json:"webhook_secret"`  // whsec_（回调验签）
	TargetCurrency string `json:"target_currency"` // 跨币目标（USD/EUR…；空=CNY 直收）
	APITestMode    bool   `json:"api_test_mode"`   // 信息性标记（key 前缀即环境）
}

// StripeAdapter 适配器。
type StripeAdapter struct{}

// NewStripe 构造。
func NewStripe() *StripeAdapter { return &StripeAdapter{} }

// Type 渠道驱动名。
func (a *StripeAdapter) Type() string { return "stripe" }

// ValidateConfig 校验凭据必填。
func (a *StripeAdapter) ValidateConfig(cfg json.RawMessage) error {
	var c stripeConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return fmt.Errorf("stripe: 凭据格式错误: %w", err)
	}
	if c.SecretKey == "" || c.WebhookSecret == "" {
		return fmt.Errorf("stripe: secret_key/webhook_secret 必填")
	}
	return nil
}

// stripeBackends 每调用构造（无全局态；测试经 testBackend 注入 httptest）。
var stripeBackendOverride stripe.Backends

func stripeClient(key string) *stripec.API {
	if stripeBackendOverride.API != nil {
		return stripec.New(key, &stripeBackendOverride)
	}
	return stripec.New(key, nil)
}

// CreatePayment Checkout Session（redirect 到收银台）。
func (a *StripeAdapter) CreatePayment(ctx context.Context, req port.CreatePaymentRequest) (*port.RedirectInfo, error) {
	var c stripeConfig
	if err := json.Unmarshal(req.Config, &c); err != nil {
		return nil, fmt.Errorf("stripe: 凭据格式错误: %w", err)
	}
	if c.SecretKey == "" {
		return nil, fmt.Errorf("stripe: secret_key 必填")
	}
	// 金额口径（ 快照）：跨币用 ChargedUnits/ChargedCurrency；同币直收回落 Amount
	units := int64(req.Amount)
	currency := "cny"
	if req.ChargedUnits > 0 {
		units = req.ChargedUnits
		currency = strings.ToLower(req.ChargedCurrency)
	} else if c.TargetCurrency != "" {
		// 快照缺席（同币直收）但渠道声明跨币目标——配置矛盾，拒绝（fail-closed：
		// 宁可拒单不错币种收款）
		return nil, fmt.Errorf("stripe: 快照缺失（currency 表未配置 %s？）", c.TargetCurrency)
	}
	successURL := req.ReturnURL
	if successURL == "" {
		successURL = "/payment/" + req.OrderNo
	}
	params := &stripe.CheckoutSessionParams{
		Mode:              stripe.String("payment"),
		ClientReferenceID: stripe.String(req.OrderNo),
		SuccessURL:        stripe.String(successURL),
		CancelURL:         stripe.String(successURL + "?status=cancel"),
		Metadata:          map[string]string{"order_no": req.OrderNo},
		PaymentIntentData: &stripe.CheckoutSessionPaymentIntentDataParams{Metadata: map[string]string{"order_no": req.OrderNo}},
		LineItems: []*stripe.CheckoutSessionLineItemParams{{
			Quantity: stripe.Int64(1),
			PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
				Currency:   stripe.String(currency),
				UnitAmount: stripe.Int64(units),
				ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
					Name: stripe.String(req.Subject),
				},
			},
		}},
	}
	params.Context = ctx
	sess, err := stripeClient(c.SecretKey).CheckoutSessions.New(params)
	if err != nil {
		return nil, fmt.Errorf("stripe: 下单失败: %w", err)
	}
	payload, _ := json.Marshal(map[string]string{"url": sess.URL})
	return &port.RedirectInfo{Type: "redirect", Payload: payload}, nil
}

// ParseWebhook Stripe-Signature 构造验证（SDK ConstructEvent，默认 tolerance）→ CallbackFact。
func (a *StripeAdapter) ParseWebhook(headers map[string]string, body []byte, cfg json.RawMessage) (*port.CallbackFact, error) {
	var c stripeConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return nil, fmt.Errorf("stripe: 凭据格式错误: %w", err)
	}
	sig := headers["Stripe-Signature"]
	if sig == "" {
		sig = headers["stripe-signature"]
	}
	if sig == "" {
		return nil, fmt.Errorf("stripe: 缺 Stripe-Signature 头")
	}
	// IgnoreAPIVersionMismatch：签名验证仍强制；仅放宽 schema 版本一致性告警
	//（网关端点版本可能滞后于 SDK——字段解析在下方防御性进行）
	event, err := webhook.ConstructEventWithOptions(body, sig, c.WebhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
	if err != nil {
		return nil, fmt.Errorf("stripe: 验签失败: %w", err)
	}
	var sess stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
		return nil, fmt.Errorf("stripe: 事件体解析失败: %w", err)
	}
	// 单号双定位（metadata 优先，client_reference_id 兜底）
	orderNo := sess.Metadata["order_no"]
	if orderNo == "" {
		orderNo = sess.ClientReferenceID
	}
	channelNo := sess.ID
	if sess.PaymentIntent != nil && sess.PaymentIntent.ID != "" {
		channelNo = sess.PaymentIntent.ID // pi_（退款锚点）
	}
	success := event.Type == "checkout.session.completed" || event.Type == "checkout.session.async_payment_succeeded"
	var amount money.Cents
	currency := strings.ToUpper(string(sess.Currency))
	if success {
		if sess.AmountTotal == 0 {
			return nil, fmt.Errorf("stripe: 事件缺 amount_total")
		}
		amount = money.Cents(sess.AmountTotal)
	}
	return &port.CallbackFact{
		Provider:       "stripe",
		ChannelOrderNo: channelNo,
		OrderNo:        orderNo,
		Amount:         amount, // 渠道币种最小单位（ 快照核对口径）
		Currency:       currency,
		Success:        success,
		Raw:            body,
	}, nil
}

// QueryPayment 主动查单补单（cs_ session，expand payment_intent）。
func (a *StripeAdapter) QueryPayment(ctx context.Context, gatewayOrderNo string, cfg json.RawMessage) (*port.CallbackFact, error) {
	var c stripeConfig
	if err := json.Unmarshal(cfg, &c); err != nil || c.SecretKey == "" {
		return nil, fmt.Errorf("stripe: 凭据错误")
	}
	params := &stripe.CheckoutSessionParams{}
	params.Expand = []*string{stripe.String("payment_intent")}
	params.Context = ctx
	sess, err := stripeClient(c.SecretKey).CheckoutSessions.Get(gatewayOrderNo, params)
	if err != nil {
		return nil, fmt.Errorf("stripe: 查单失败: %w", err)
	}
	orderNo := sess.Metadata["order_no"]
	if orderNo == "" {
		orderNo = sess.ClientReferenceID
	}
	channelNo := sess.ID
	if sess.PaymentIntent != nil && sess.PaymentIntent.ID != "" {
		channelNo = sess.PaymentIntent.ID
	}
	success := sess.PaymentStatus == stripe.CheckoutSessionPaymentStatusPaid
	var amount money.Cents
	if success {
		amount = money.Cents(sess.AmountTotal)
	}
	return &port.CallbackFact{
		Provider:       "stripe",
		ChannelOrderNo: channelNo,
		OrderNo:        orderNo,
		Amount:         amount,
		Currency:       strings.ToUpper(string(sess.Currency)),
		Success:        success,
	}, nil
}

// Refund 原路退款（Refunds API）。amount 语义 = 渠道币种最小单位（charged_units
// 快照——实收原币退，调用方从支付单快照取数；跨境退款零二次换算）。
func (a *StripeAdapter) Refund(ctx context.Context, gatewayOrderNo string, amount money.Cents, reason string, cfg json.RawMessage) error {
	var c stripeConfig
	if err := json.Unmarshal(cfg, &c); err != nil || c.SecretKey == "" {
		return fmt.Errorf("stripe: 凭据错误")
	}
	if !strings.HasPrefix(gatewayOrderNo, "pi_") {
		return fmt.Errorf("stripe: 退款锚点须为 payment_intent（pi_）")
	}
	params := &stripe.RefundParams{
		PaymentIntent: stripe.String(gatewayOrderNo),
		Reason:        stripe.String("requested_by_customer"),
	}
	if int64(amount) > 0 {
		params.Amount = stripe.Int64(int64(amount))
	}
	if reason != "" {
		params.Metadata = map[string]string{"reason": reason}
	}
	params.Context = ctx
	if _, err := stripeClient(c.SecretKey).Refunds.New(params); err != nil {
		return fmt.Errorf("stripe: 退款失败: %w", err)
	}
	return nil
}
