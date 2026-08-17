// Package port 为 payment 模块对外契约（零依赖包）。
//
// 渠道能力接口拆分（§5.5.1，采纳友商形态，替代 1.x 大 PaymentDriver）：
// 按 (provider, channel) 注册表路由；新渠道 = 新增 adapter 文件 + 注册，不改核心代码。
package port

import (
	"context"
	"encoding/json"

	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// CreatePaymentRequest 创建支付请求。
type CreatePaymentRequest struct {
	OrderNo        string
	Channel        string // 渠道码
	Amount         money.Cents
	Subject        string
	ReturnURL      string
	NotifyBaseURL  string
	IdempotencyKey string          // 写接口幂等（§7.3）
	Config         json.RawMessage // 解密后的渠道凭据 JSON（每渠道独立，adapter 无状态）
}

// RedirectInfo 支付发起结果（收银台/二维码/参数包）。
type RedirectInfo struct {
	Type    string // redirect / qrcode / params
	Payload json.RawMessage
}

// RechargePaymentInfo 充值支付单发起结果（wallet 模块消费）。
type RechargePaymentInfo struct {
	PaymentID uint64
	Type      string // redirect / qrcode / params
	Payload   string
}

// RechargePayer 充值支付单创建端口（wallet 模块消费，通道 A）：
// 充值单落 pending 后创建支付单（关联 recharge_order_id）+ 渠道发起，
// 余额入账只发生在回调成功后（铁律 16）。
type RechargePayer interface {
	CreateRechargePayment(ctx context.Context, rechargeOrderID uint64, channel string, amount money.Cents) (*RechargePaymentInfo, error)
}

// CallbackFact 回调事实（VerifyCallback/ParseWebhook 的统一产出，四重校验的输入）。
type CallbackFact struct {
	Provider       string
	ChannelOrderNo string // 网关单号（独立字段隔离下游回传格式）
	OrderNo        string // 业务单号
	Amount         money.Cents
	Currency       string
	Success        bool
	Raw            json.RawMessage // 回调原文（审计）
}

// Provider 渠道基础能力（所有渠道必须实现）。
type Provider interface {
	Type() string
	ValidateConfig(cfg json.RawMessage) error
	CreatePayment(ctx context.Context, req CreatePaymentRequest) (*RedirectInfo, error)
}

// Webhooker JSON webhook 解析（stripe/paypal 类）。
type Webhooker interface {
	ParseWebhook(headers map[string]string, body []byte) (*CallbackFact, error)
}

// CallbackVerifier 表单同步回调验签（epay/alipay 类）。
type CallbackVerifier interface {
	VerifyCallback(form map[string]string, cfg json.RawMessage) (*CallbackFact, error)
}

// Capturer 主动查单补单（1.x「手动补单」的规范化）。
type Capturer interface {
	QueryPayment(ctx context.Context, gatewayOrderNo string, cfg json.RawMessage) (*CallbackFact, error)
}

// Refunder 原路退款（2.0 新增能力位，退款编排三通道之一）。
type Refunder interface {
	Refund(ctx context.Context, gatewayOrderNo string, amount money.Cents, reason string, cfg json.RawMessage) error
}

// OrderRefunder 订单退款入口（P2-02 procurement 失败策略消费，通道 A）：
// 按订单创建退款单（channel=upstream：货源采购失败自动退款），
// 由 payment 模块驱动订单 refund 流转。
type OrderRefunder interface {
	RefundOrder(ctx context.Context, orderID uint64, amount money.Cents, reason string) error
}

// SlowPaymentChecker 慢通道 pending 流水探测（P1-03 order 超时取消顺延判定，通道 A）：
// usdt 族链上确认慢于订单 TTL——存在 pending 流水时超时任务顺延不误杀。
type SlowPaymentChecker interface {
	HasPendingSlowPayment(ctx context.Context, orderID uint64) (bool, error)
}

// Registry 渠道注册表（按 (provider, channel) 路由；支付渠道配置存 payment_channels）。
type Registry interface {
	Register(p Provider)
	Provider(provider string) (Provider, error)
}
