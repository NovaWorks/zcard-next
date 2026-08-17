package settings

// 设置分组目录（P0-04 T1）：每分组强类型默认值 + SECRET/PUBLIC 双清单 + 键校验。
// 运行时业务开关的真理源在 settings 表（铁律 7）；本目录是「键合法性 + 默认值 + 脱敏」的唯一裁决。

import (
	"encoding/json"
	"fmt"
	"sort"
)

// GroupDef 分组定义。
type GroupDef struct {
	Name string
	Desc string
	// Defaults 默认值（key → JSON 值）；GetStruct 缺省回落
	Defaults map[string]any
	// SecretKeys 敏感键：读接口脱敏 ****（写回时 **** 视为未修改），前台绝不下发
	SecretKeys map[string]bool
	// PublicKeys 前台白名单键（GetPublicConfig 仅下发这些；不在其中的键绝不外泄）
	PublicKeys map[string]bool
}

// groups 全部分组目录（主文档 §5.15 清裁）。
var groups = map[string]*GroupDef{
	"site": {
		Name: "site", Desc: "站点基础",
		Defaults: map[string]any{
			"name":         "ZCard 演示站",
			"logo":         "",
			"url":          "",
			"seo_title":    "",
			"seo_keywords": "",
			"seo_desc":     "",
			"admin_path":   "",
			"top_button":   nil, // 顶部自定义按钮 {text,url}
		},
		PublicKeys: map[string]bool{"name": true, "logo": true, "url": true, "seo_title": true, "seo_keywords": true, "seo_desc": true},
	},
	"template": {
		Name: "template", Desc: "模板",
		Defaults: map[string]any{
			"pc_template":        "classic",
			"mobile_template":    "classic",
			"bg_image":           "",
			"category_nav_style": "list", // list | grid
			"default_view":       "grid", // list | grid | big
			"per_row":            4,
			"per_page":           20,
			"sort_by":            "default",
			"show_stock":         true,
			"show_sales":         true,
		},
		PublicKeys: map[string]bool{"pc_template": true, "mobile_template": true, "category_nav_style": true, "default_view": true, "per_row": true, "per_page": true, "sort_by": true, "show_stock": true, "show_sales": true},
	},
	"footer": {
		Name: "footer", Desc: "页脚",
		Defaults: map[string]any{
			"about":     "",
			"nav":       nil, // [{text,url}]
			"agreement": "",
			"contact":   "",
			"social":    nil, // [{icon,url}]
			"icp":       "",
		},
		PublicKeys: map[string]bool{"about": true, "agreement": true, "contact": true, "icp": true},
	},
	"promo": {
		Name: "promo", Desc: "推荐位（落地行为 content/banners）",
		Defaults: map[string]any{
			"top_banner_enabled": true,
			"nav_recommend":      nil,
		},
		PublicKeys: map[string]bool{"top_banner_enabled": true},
	},
	"trade": {
		Name: "trade", Desc: "交易",
		Defaults: map[string]any{
			"guest_checkout":    true,
			"contact_required":  "any", // none | phone | email | qq | any
			"query_password":    true,
			"order_ttl_minutes": 30,
			"cart_enabled":      true,
			"api_order_enabled": false,
		},
	},
	"security": {
		Name: "security", Desc: "安全",
		Defaults: map[string]any{
			"register_enabled":   true,
			"register_method":    "username", // username | email | phone
			"captcha_register":   true,
			"captcha_login":      false,
			"captcha_order":      false,
			"captcha_reset":      true,
			"username_min_len":   3,
			"max_pending_per_ip": 3,
			"risk_enabled":       true,
		},
	},
	"ops": {
		Name: "ops", Desc: "运维",
		Defaults: map[string]any{
			"maintenance":       false,
			"maintenance_style": "modal", // modal | banner
			"announcement_type": "text",  // text | image | carousel
			"announcement":      "",
			"installed_at":      nil, // 安装时间（install 写入，业务只读）
		},
		PublicKeys: map[string]bool{"maintenance": true, "announcement_type": true, "announcement": true},
	},
	"recharge": {
		Name: "recharge", Desc: "充值",
		Defaults: map[string]any{
			"enabled":    true,
			"min_amount": 1000, // 分
			"max_amount": 500000,
			"gift_tiers": nil, // [{amount,gift_balance,gift_points}]
		},
	},
	"license": {
		Name: "license", Desc: "订阅许可证",
		Defaults: map[string]any{
			"file":        "", // 许可证内容（JSON；安装时写入）
			"pubkey":      "", // ed25519 公钥（base64；发行侧配置）
			"domain":      "", // 主站域名（许可证绑定校验；空=跳过）
			"instance_id": "", // 实例 ID（首次读取时生成并持久化）
			// P3-08 在线购买（发行侧部署配置；M3「专业套餐可购买激活」）
			"purchase_monthly_cents": 300,  // 月付 3U
			"purchase_yearly_cents":  3000, // 年付 30U
			"purchase_privkey":       "",   // ed25519 签发私钥（base64；空=不开通在线购买）
		},
		SecretKeys: map[string]bool{"purchase_privkey": true},
	},
	"withdraw": {
		Name: "withdraw", Desc: "提现",
		Defaults: map[string]any{
			"enabled":    false,
			"min_amount": 1000,
			"fee_type":   "fixed", // fixed | percent
			"fee_value":  0,
			"methods":    nil, // 白名单 [{type,name,account}]
		},
	},
	"points": {
		Name: "points", Desc: "积分",
		Defaults: map[string]any{
			"enabled":        true,
			"deduct_rate":    100, // X 积分抵 1 分
			"max_deduct_pct": 50,  // 单单最大抵扣比例
		},
	},
	"affiliate": {
		Name: "affiliate", Desc: "分销",
		Defaults: map[string]any{
			"enabled":      false,
			"levels":       3,
			"rate_l1":      500, // 万分比
			"rate_l2":      200,
			"rate_l3":      100,
			"base":         "amount", // amount | profit
			"confirm_days": 7,
			"self_buy":     false,
		},
	},
	"supply": {
		Name: "supply", Desc: "货源",
		Defaults: map[string]any{
			"import_max_lines":      100000,
			"low_stock_threshold":   5,
			"sync_interval_minutes": 30,
		},
	},
	"notify": {
		Name: "notify", Desc: "邮件短信",
		Defaults: map[string]any{
			"smtp_host":     "",
			"smtp_port":     465,
			"smtp_user":     "",
			"smtp_password": "",
			"smtp_name":     "",
			"sms_provider":  "",
			"sms_key":       "",
			"sms_secret":    "",
			"sms_sign":      "",
		},
		SecretKeys: map[string]bool{"smtp_password": true, "sms_key": true, "sms_secret": true},
	},
	"i18n": {
		Name: "i18n", Desc: "语言货币",
		Defaults: map[string]any{
			"default_locale":  "zh_CN",
			"enabled_locales": []string{"zh_CN"},
			"base_currency":   "CNY",
		},
		PublicKeys: map[string]bool{"default_locale": true, "enabled_locales": true, "base_currency": true},
	},
}

// trade 组 PublicKeys 在 init 内补充（同文件上方字段初始化保持简洁）
func init() {
	groups["trade"].PublicKeys = map[string]bool{
		"guest_checkout": true, "query_password": true, "contact_required": true,
	}
}

// mustJSON 序列化（目录默认值为字面量，序列化不会失败）。
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// Group 取分组定义。
func Group(name string) (*GroupDef, bool) {
	g, ok := groups[name]
	return g, ok
}

// GroupsSorted 全部分组名（稳定排序）。
func GroupsSorted() []string {
	out := make([]string, 0, len(groups))
	for k := range groups {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ValidateKey 键校验：分组存在 + 键在默认值目录内（未知键拒绝，防脏数据）。
func ValidateKey(group, key string) error {
	g, ok := groups[group]
	if !ok {
		return fmt.Errorf("settings: 未知分组 %q（合法：%v）", group, GroupsSorted())
	}
	if _, ok := g.Defaults[key]; !ok {
		return fmt.Errorf("settings: 分组 %q 内未知键 %q", group, key)
	}
	return nil
}

// IsSecret 是否敏感键。
func IsSecret(group, key string) bool {
	g, ok := groups[group]
	return ok && g.SecretKeys[key]
}

// IsPublic 是否前台白名单键。
func IsPublic(group, key string) bool {
	g, ok := groups[group]
	return ok && g.PublicKeys[key]
}

// PublicKeysOf 分组前台键清单（测试快照用）。
func PublicKeysOf(group string) []string {
	g, ok := groups[group]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(g.PublicKeys))
	for k := range g.PublicKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DefaultJSON 分组默认值整体 JSON（GetStruct 兜底与 install 初始写入用）。
func (g *GroupDef) DefaultJSON(key string) (string, bool) {
	v, ok := g.Defaults[key]
	if !ok {
		return "", false
	}
	return mustJSON(v), true
}
