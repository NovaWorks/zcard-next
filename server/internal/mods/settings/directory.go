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
	// Labels 业务显示名称（key → 中文名；admin 列表/表单渲染，缺失回落 key 本身）
	Labels map[string]string
	// Options 枚举选项（key → value → 展示名；admin 表单渲染下拉）
	Options map[string]map[string]string
	// SecretKeys 敏感键：读接口脱敏 ****（写回时 **** 视为未修改），前台绝不下发
	SecretKeys map[string]bool
	// PublicKeys 前台白名单键（GetPublicConfig 仅下发这些；不在其中的键绝不外泄）
	PublicKeys map[string]bool
}

// groups 全部分组目录（主文档 §5.15 清裁）。
var groups = map[string]*GroupDef{
	"site": {
		Name: "site", Desc: "站点基础",
		Labels: map[string]string{
			"name": "站点名称", "logo": "站点 Logo", "url": "站点地址",
			"seo_title": "SEO 标题", "seo_keywords": "SEO 关键词", "seo_desc": "SEO 描述",
			"verification_google": "Google 站长验证码", "verification_bing": "Bing 站长验证码",
			"robots_custom": "robots.txt 自定义规则",
			"admin_path": "后台安全路径", "top_button": "顶部自定义按钮",
		},
		Defaults: map[string]any{
			"name":         "ZCard 演示站",
			"logo":         "",
			"url":          "",
			"seo_title":    "",
			"seo_keywords": "",
			"seo_desc":     "",
			"verification_google": "",
			"verification_bing":    "",
			"robots_custom":        "",
			"admin_path":   "",
			"top_button":   nil, // 顶部自定义按钮 {text,url}
		},
		PublicKeys: map[string]bool{"name": true, "logo": true, "url": true, "seo_title": true, "seo_keywords": true, "seo_desc": true, "verification_google": true, "verification_bing": true, "robots_custom": true, "top_button": true},
	},
	"template": {
		Name: "template", Desc: "模板",
		Labels: map[string]string{
			"pc_template": "PC 端模板", "mobile_template": "移动端模板", "bg_image": "背景图",
			"category_nav_style": "分类导航样式", "default_view": "商品默认视图",
			"per_row": "每行商品数", "per_page": "每页商品数", "sort_by": "默认排序方式",
			"show_stock": "显示库存", "show_sales": "显示销量", "show_reviews": "显示评价",
		},
		Options: map[string]map[string]string{
			"category_nav_style": {"list": "列表", "grid": "网格"},
			"default_view":       {"list": "列表", "grid": "网格", "big": "大图"},
			"sort_by": {
				"default":    "综合排序（默认）",
				"sales":      "销量优先",
				"newest":     "最新上架",
				"price_asc":  "价格从低到高",
				"price_desc": "价格从高到低",
			},
		},
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
			"show_reviews":       true,
		},
		PublicKeys: map[string]bool{"pc_template": true, "mobile_template": true, "bg_image": true, "category_nav_style": true, "default_view": true, "per_row": true, "per_page": true, "sort_by": true, "show_stock": true, "show_sales": true, "show_reviews": true},
	},
	"footer": {
		Name: "footer", Desc: "页脚",
		Labels: map[string]string{
			"about": "页脚关于", "nav": "页脚导航", "agreement": "用户协议",
			"contact": "联系方式", "social": "社交链接", "icp": "ICP 备案号",
		},
		Defaults: map[string]any{
			"about":     "",
			"nav":       nil, // [{text,url}]
			"agreement": "",
			"contact":   "",
			"social":    nil, // [{icon,url}]
			"icp":       "",
		},
		PublicKeys: map[string]bool{"about": true, "nav": true, "agreement": true, "contact": true, "social": true, "icp": true},
	},
	"promo": {
		Name: "promo", Desc: "推荐位（落地行为 content/banners）",
		Labels: map[string]string{"top_banner_enabled": "顶部横幅开关", "nav_recommend": "导航推荐位"},
		Defaults: map[string]any{
			"top_banner_enabled": true,
			"nav_recommend":      nil,
		},
		PublicKeys: map[string]bool{"top_banner_enabled": true, "nav_recommend": true},
	},
	// 客户代码（service）：第三方 JS 嵌入——客服代码（悬浮球）与统计代码（页面底部）
	"service": {
		Name: "service", Desc: "客服与统计代码",
		Labels: map[string]string{
			"widget_script": "客服代码（Chatwoot/Crisp 等嵌入代码）",
			"stats_script":  "统计代码（百度/GA/51la，页面底部注入）",
		},
		Defaults: map[string]any{
			"widget_script": "", // 客服嵌入代码（含 <script> 标签）；非空时前台显示第三方客服悬浮球
			"stats_script":  "", // 统计代码（body 末尾注入；百度/GA/51la 等）
		},
		PublicKeys: map[string]bool{
			"widget_script": true, "stats_script": true,
		},
	},
	"trade": {
		Name: "trade", Desc: "交易",
		Labels: map[string]string{
			"guest_checkout": "游客下单", "contact_required": "联系方式要求", "query_password": "订单查询密码",
			"order_ttl_minutes": "订单超时（分钟）", "cart_enabled": "购物车功能", "api_order_enabled": "API 下单",
		},
		Options: map[string]map[string]string{
			"contact_required": {"none": "不要求", "phone": "手机号", "email": "邮箱", "qq": "QQ", "any": "任意一种"},
		},
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
		Labels: map[string]string{
			"register_enabled": "开放注册", "register_method": "注册方式（多选）",
			"captcha_register": "注册验证码", "captcha_login": "登录验证码", "captcha_order": "下单验证码",
			"captcha_reset": "重置密码验证码", "captcha_admin_login": "后台登录验证码",
			"username_min_len":   "用户名最小长度",
			"max_pending_per_ip": "单 IP 待付款订单上限", "risk_enabled": "风控拦截",
		},
		Options: map[string]map[string]string{
			// 多选（数组）：勾选的通道全部可用；至少含 username 才能免验证注册
			"register_method": {"username": "用户名", "email": "邮箱验证", "phone": "手机验证"},
		},
		Defaults: map[string]any{
			"register_enabled":    true,
			"register_method":     []string{"username"},
			"captcha_register":    true,
			"captcha_login":       false,
			"captcha_order":       false,
			"captcha_reset":       true,
			"captcha_admin_login": false,
			"username_min_len":    3,
			"max_pending_per_ip":  3,
			"risk_enabled":        true,
		},
	},
	"ops": {
		Name: "ops", Desc: "运维",
		Labels: map[string]string{
			"maintenance": "维护模式", "maintenance_style": "维护提示样式",
			"maintenance_modal_freq": "维护弹窗频率",
			"announcement_type": "公告类型", "announcement": "公告内容",
			"installed_at": "安装时间（只读）",
		},
		Options: map[string]map[string]string{
			"maintenance_style": {"modal": "弹窗", "banner": "顶部横幅"},
			// 弹窗样式下的自动弹出频率（banner 样式不生效）
			"maintenance_modal_freq": {"every": "每次进入都弹", "daily": "24 小时内只弹一次"},
			"announcement_type":      {"text": "文本", "image": "图片", "carousel": "轮播"},
		},
		Defaults: map[string]any{
			"maintenance":             false,
			"maintenance_style":       "modal", // modal | banner
			"maintenance_modal_freq":  "every", // every | daily（仅 modal 样式生效）
			"announcement_type":       "text",  // text | image | carousel
			"announcement":            "",
			"installed_at":            nil, // 安装时间（install 写入，业务只读）
		},
		PublicKeys: map[string]bool{
			"maintenance": true, "maintenance_style": true, "maintenance_modal_freq": true,
			"announcement_type": true, "announcement": true,
		},
	},
	"recharge": {
		Name: "recharge", Desc: "充值",
		Labels: map[string]string{
			"enabled": "充值功能", "min_amount": "最小充值金额（分）",
			"max_amount": "最大充值金额（分）", "gift_tiers": "充值赠送档位",
		},
		Defaults: map[string]any{
			"enabled":    true,
			"min_amount": 1000, // 分
			"max_amount": 500000,
			"gift_tiers": nil, // [{amount,gift_balance,gift_points}]
		},
		// 前台公开：充值页档位/赠送规则展示（P3-09；金额裁决仍在服务端）
		PublicKeys: map[string]bool{"enabled": true, "min_amount": true, "max_amount": true, "gift_tiers": true},
	},
	"supplier_recharge": {
		Name: "supplier_recharge", Desc: "供货充值",
		Labels: map[string]string{
			"enabled": "供货充值功能", "min_amount": "最小充值金额（分）",
			"max_amount": "最大充值金额（分）",
			"gift_tiers": "充值赠送档位",
		},
		Defaults: map[string]any{
			"enabled":    true,
			"min_amount": 1000, // 分（独立于钱包充值档位）
			"max_amount": 500000,
			"gift_tiers": nil, // [{amount,gift_balance}] 充满 amount 分赠 gift_balance 分供货余额
		},
		// 前台公开：对接账户充值弹窗限额与赠送档位展示（金额裁决仍在服务端）
		PublicKeys: map[string]bool{"enabled": true, "min_amount": true, "max_amount": true, "gift_tiers": true},
	},
	"license": {
		Name: "license", Desc: "订阅许可证",
		Labels: map[string]string{
			"file": "许可证内容", "pubkey": "许可证公钥", "domain": "绑定域名",
			"instance_id": "实例 ID", "purchase_monthly_cents": "月付价格（分）",
			"purchase_yearly_cents": "年付价格（分）", "purchase_privkey": "购买签发私钥",
		},
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
		Labels: map[string]string{
			"enabled": "提现功能", "min_amount": "最小提现金额（分）",
			"fee_type": "手续费方式", "fee_value": "手续费值", "methods": "收款方式白名单",
		},
		Options: map[string]map[string]string{
			"fee_type": {"fixed": "固定金额", "percent": "按比例"},
		},
		Defaults: map[string]any{
			"enabled":    false,
			"min_amount": 1000,
			"fee_type":   "fixed", // fixed | percent
			"fee_value":  0,
			"methods": []map[string]string{
				{"type": "alipay", "name": "支付宝"},
				{"type": "wechat", "name": "微信"},
				{"type": "usdt_trc20", "name": "USDT TRC20"},
			}, // 白名单 [{type,name}]
		},
	},
	"points": {
		Name: "points", Desc: "积分",
		Labels: map[string]string{
			"enabled": "积分功能", "deduct_rate": "积分抵现比例", "max_deduct_pct": "最大抵扣比例（%）",
		},
		Defaults: map[string]any{
			"enabled":        true,
			"deduct_rate":    100, // X 积分抵 1 分
			"max_deduct_pct": 50,  // 单单最大抵扣比例
		},
	},
	"affiliate": {
		Name: "affiliate", Desc: "分销",
		Labels: map[string]string{
			"enabled": "分销功能", "levels": "分销层级",
			"rate_l1": "L1 佣金（万分比）", "rate_l2": "L2 佣金（万分比）", "rate_l3": "L3 佣金（万分比）",
			"base": "佣金基数", "confirm_days": "确认收货天数", "self_buy": "允许自购",
		},
		Options: map[string]map[string]string{
			"base": {"amount": "订单金额", "profit": "利润"},
		},
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
		Labels: map[string]string{
			"import_max_lines": "导入最大行数", "low_stock_threshold": "低库存阈值",
			"sync_interval_minutes": "同步间隔（分钟）",
		},
		Defaults: map[string]any{
			"import_max_lines":      100000,
			"low_stock_threshold":   5,
			"sync_interval_minutes": 30,
		},
	},
	"notify": {
		Name: "notify", Desc: "邮件短信",
		Labels: map[string]string{
			"smtp_host": "SMTP 服务器", "smtp_port": "SMTP 端口", "smtp_user": "SMTP 用户名",
			"smtp_password": "SMTP 密码", "smtp_name": "发件人名称",
			"sms_provider": "短信服务商", "sms_key": "短信 AccessKey",
			"sms_secret":            "短信 SecretKey",
			"sms_sign":              "短信签名",
			"sms_sdk_app_id":        "短信 SDK AppID",
			"sms_template_code":     "短信模板 ID",
			"sms_template_register": "注册验证码短信模板（内容需与通道模板一致；变量 {code}{minutes}{site}）",
			"sms_template_reset":    "找回密码短信模板（内容需与通道模板一致；变量 {code}{minutes}{site}）",
		},
		Options: map[string]map[string]string{
			"sms_provider": {"aliyun": "阿里云短信", "tencent": "腾讯云短信", "qiniu": "七牛短信"},
		},
		Defaults: map[string]any{
			"smtp_host":      "",
			"smtp_port":      465,
			"smtp_user":      "",
			"smtp_password":  "",
			"smtp_name":      "",
			"sms_provider":   "aliyun",
			"sms_key":        "",
			"sms_secret":     "",
			"sms_sign":       "",
			"sms_sdk_app_id": "",
			// 模板变量：{code} 验证码 {minutes} 有效分钟 {site} 站点名
			"sms_template_code":     "",
			"sms_template_register": "【{site}】您的注册验证码：{code}，{minutes} 分钟内有效。",
			"sms_template_reset":    "【{site}】您正在重置密码，验证码：{code}，{minutes} 分钟内有效。",
		},
		SecretKeys: map[string]bool{"smtp_password": true, "sms_key": true, "sms_secret": true},
	},
	"i18n": {
		Name: "i18n", Desc: "语言货币",
		Labels: map[string]string{
			"default_locale": "默认语言", "enabled_locales": "启用语言列表", "base_currency": "基础货币",
		},
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
	// withdraw 组公开键（提现页表单驱动：开关/最低额/手续费/白名单）
	groups["withdraw"].PublicKeys = map[string]bool{
		"enabled": true, "min_amount": true, "fee_type": true, "fee_value": true, "methods": true,
	}
	// security 组公开键：注册页动态表单渲染（开关/方式）+ 图形验证码场景开关（前端条件渲染）
	groups["security"].PublicKeys = map[string]bool{
		"register_enabled": true, "register_method": true,
		"captcha_register": true, "captcha_login": true, "captcha_order": true, "captcha_reset": true,
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
