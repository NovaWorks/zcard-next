package adapter

// 内置驱动元数据与配置字段 schema（P2-09 T5：admin 配置面动态表单渲染）。
// 新渠道 = adapter 文件 + Meta/ConfigFields 声明，前端零改动。
// 敏感字段（Sensitive）编辑时留空=保持原值（不回显不覆盖——老项目 sysadmin 同规）；
// 回调地址不在此配置——后端按站点 URL 拼接，配置面板底部展示复制。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
)

// Meta 支付宝。
func (a *AlipayAdapter) Meta() port.DriverMeta {
	return port.DriverMeta{Name: "支付宝", Icon: "alipay", Description: "支付宝网页支付（需商户签约 RSA2 密钥）"}
}

// ConfigFields 支付宝配置字段。
func (a *AlipayAdapter) ConfigFields() []port.ConfigField {
	return []port.ConfigField{
		{Key: "app_id", Label: "App ID", Type: "text", Required: true, Placeholder: "应用 APPID"},
		{Key: "private_key", Label: "商户私钥", Type: "password", Required: true, Sensitive: true, Help: "应用私钥 PEM（RSA2），用于请求签名"},
		{Key: "alipay_public_key", Label: "支付宝公钥", Type: "password", Required: true, Sensitive: true, Help: "支付宝公钥 PEM，用于回调验签"},
		{Key: "gateway", Label: "网关地址", Type: "text", Default: "https://openapi.alipay.com/gateway.do", Placeholder: "留空使用官方网关"},
	}
}

// Meta 微信支付。
func (a *WechatAdapter) Meta() port.DriverMeta {
	return port.DriverMeta{Name: "微信支付", Icon: "wechat", Description: "微信 Native 扫码支付"}
}

// ConfigFields 微信支付配置字段。
func (a *WechatAdapter) ConfigFields() []port.ConfigField {
	return []port.ConfigField{
		{Key: "app_id", Label: "AppID", Type: "text", Required: true, Placeholder: "微信开放平台/商户平台 AppID"},
		{Key: "mch_id", Label: "商户号", Type: "text", Required: true, Placeholder: "微信支付商户号 MchID"},
		{Key: "api_key", Label: "API 密钥", Type: "password", Required: true, Sensitive: true, Help: "商户平台设置的 APIv2 密钥"},
		{Key: "sign_type", Label: "签名方式", Type: "select", Default: "MD5", Options: []port.ConfigOption{{Label: "MD5", Value: "MD5"}, {Label: "HMAC-SHA256", Value: "HMAC-SHA256"}}},
	}
}

// Meta 易支付。
func (a *EpayAdapter) Meta() port.DriverMeta {
	return port.DriverMeta{Name: "易支付", Icon: "epay", Description: "易支付聚合网关（需对接第三方平台）"}
}

// ConfigFields 易支付配置字段。
func (a *EpayAdapter) ConfigFields() []port.ConfigField {
	return []port.ConfigField{
		{Key: "pid", Label: "商户号", Type: "text", Required: true, Placeholder: "平台分配的商户 ID"},
		{Key: "key", Label: "商户密钥", Type: "password", Required: true, Sensitive: true, Help: "平台商户密钥，用于签名"},
		{Key: "api_url", Label: "下单网关", Type: "text", Required: true, Placeholder: "如 https://pay.xxx.com/submit.php"},
	}
}

// Meta epusdt（GMPay）。
func (a *EpusdtAdapter) Meta() port.DriverMeta {
	return port.DriverMeta{Name: "USDT（TRC20）", Icon: "epusdt", Description: "自托管 epusdt 网关，USDT 链上收款"}
}

// ConfigFields epusdt 配置字段。
func (a *EpusdtAdapter) ConfigFields() []port.ConfigField {
	return []port.ConfigField{
		{Key: "api_url", Label: "网关地址", Type: "text", Required: true, Placeholder: "如 https://epay.example.com"},
		{Key: "pid", Label: "商户 ID", Type: "text", Required: true, Placeholder: "网关后台分配的 PID"},
		{Key: "secret_key", Label: "API 密钥", Type: "password", Required: true, Sensitive: true, Help: "网关后台的密钥（HMAC 签名）"},
		{Key: "currency", Label: "法币计价", Type: "select", Default: "cny", Options: []port.ConfigOption{{Label: "CNY 人民币", Value: "cny"}, {Label: "USD 美元", Value: "usd"}}},
		{Key: "token", Label: "支付代币", Type: "text", Default: "USDT", Placeholder: "USDT"},
		{Key: "network", Label: "网络", Type: "text", Default: "TRC20", Placeholder: "TRC20"},
	}
}

// Meta stripe。
func (a *StripeAdapter) Meta() port.DriverMeta {
	return port.DriverMeta{Name: "Stripe（Visa/万事达）", Icon: "stripe", Description: "国际信用卡收单，Checkout 收银台"}
}

// ConfigFields stripe 配置字段。
func (a *StripeAdapter) ConfigFields() []port.ConfigField {
	return []port.ConfigField{
		{Key: "secret_key", Label: "Secret Key", Type: "password", Required: true, Sensitive: true, Placeholder: "sk_live_ / sk_test_", Help: "Stripe 后台 API 密钥"},
		{Key: "webhook_secret", Label: "Webhook Secret", Type: "password", Required: true, Sensitive: true, Placeholder: "whsec_", Help: "Webhook 签名密钥——回调地址填到 Stripe 后台后生成"},
		{Key: "target_currency", Label: "收款币种", Type: "text", Placeholder: "USD / EUR…", Help: "跨币收款目标（留空=CNY 直收）。需在汇率表中配置该币种"},
	}
}

// Meta paypal。
func (a *PaypalAdapter) Meta() port.DriverMeta {
	return port.DriverMeta{Name: "PayPal", Icon: "paypal", Description: "PayPal 国际收款（Orders v2）"}
}

// ConfigFields paypal 配置字段。
func (a *PaypalAdapter) ConfigFields() []port.ConfigField {
	return []port.ConfigField{
		{Key: "client_id", Label: "Client ID", Type: "text", Required: true, Placeholder: "REST API Client ID"},
		{Key: "client_secret", Label: "Client Secret", Type: "password", Required: true, Sensitive: true, Help: "REST API 密钥"},
		{Key: "mode", Label: "运行环境", Type: "select", Default: "live", Options: []port.ConfigOption{{Label: "正式环境", Value: "live"}, {Label: "沙箱环境", Value: "sandbox"}}},
		{Key: "webhook_id", Label: "Webhook ID", Type: "password", Sensitive: true, Placeholder: "留空=不校验 webhook", Help: "PayPal 后台 Webhook 签名校验 ID——回调地址填到后台后获取"},
		{Key: "brand_name", Label: "收银台品牌", Type: "text", Placeholder: "留空使用 PayPal 默认", Help: "PayPal 授权页展示的品牌名"},
		{Key: "target_currency", Label: "收款币种", Type: "text", Placeholder: "USD / EUR…", Help: "跨币收款目标（留空=CNY 直收）。需在汇率表中配置该币种"},
	}
}
