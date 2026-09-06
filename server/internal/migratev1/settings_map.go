package migratev1

// 1.x settings（group=storefront）→ 2.0 settings(group,key) 首批映射表。
// 与 mods/settings/directory.go 的组定义逐 key 对照；未命中的 key 原样进
// group=v1_legacy（不丢数据，人工决定去向）；SECRET 类显式跳过（在 2.0 后台重配）。
// 完整版随 P1 交付逐 key 补齐（《数据迁移工具开发计划》附录 C）。

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// settingRule 单条映射规则：目标组/键 + 值规范化类型。
type settingRule struct {
	Group string
	Key   string
	Kind  string // bool | int | string | enum_contact | passthrough
}

// settingsMap 首批映射（键 = 1.x settings.key）。
var settingsMap = map[string]settingRule{
	// site
	"site_name":       {Group: "site", Key: "name", Kind: "string"},
	"site_logo":       {Group: "site", Key: "logo", Kind: "string"},
	"seo_title":       {Group: "site", Key: "seo_title", Kind: "string"},
	"seo_keywords":    {Group: "site", Key: "seo_keywords", Kind: "string"},
	"seo_description": {Group: "site", Key: "seo_desc", Kind: "string"},
	// ops
	"maintenance_mode":    {Group: "ops", Key: "maintenance", Kind: "bool"},
	"maintenance_message": {Group: "ops", Key: "announcement", Kind: "string"},
	// trade
	"guest_checkout":       {Group: "trade", Key: "guest_checkout", Kind: "bool"},
	"order_query_password": {Group: "trade", Key: "query_password", Kind: "bool"},
	"order_close_minutes":  {Group: "trade", Key: "order_ttl_minutes", Kind: "int"},
	"require_contact":      {Group: "trade", Key: "contact_required", Kind: "enum_contact"},
	// security
	"trade_captcha":       {Group: "security", Key: "captcha_order", Kind: "bool"},
	"register_open":       {Group: "security", Key: "register_enabled", Kind: "bool"},
	"captcha_register":    {Group: "security", Key: "captcha_register", Kind: "bool"},
	"captcha_login":       {Group: "security", Key: "captcha_login", Kind: "bool"},
	"username_min_length": {Group: "security", Key: "username_min_len", Kind: "int"},
	// template（1.x 外观列表类）
	"category_nav_style": {Group: "template", Key: "category_nav_style", Kind: "string"},
	"list_default_view":  {Group: "template", Key: "default_view", Kind: "string"},
	"grid_columns":       {Group: "template", Key: "per_row", Kind: "int"},
	"page_size":          {Group: "template", Key: "per_page", Kind: "int"},
}

// settingsSkipped 显式不迁的 key（SECRET 类 / 1.x 专有加密开关）。
var settingsSkipped = map[string]string{
	"card_encryption_key":       "1.x 卡密钥匙（仅迁移期使用，绝不写入 2.0）",
	"card_encryption_enabled":   "1.x 卡密加密开关（2.0 强制加密，无此开关）",
	"mail_password":             "SMTP 密码（SECRET，请在 2.0 后台「通知设置」重配）",
	"sms_access_key":            "短信 AK（SECRET，请在 2.0 后台重配）",
	"sms_access_secret":         "短信 AS（SECRET，请在 2.0 后台重配）",
	"admin_alert_tg_token":      "管理员 TG 告警 token（SECRET，请在 2.0 后台重配）",
	"admin_alert_wecom_webhook": "管理员企微 webhook（SECRET，请在 2.0 后台重配）",
	"footer_analytics":          "已弃用（1.x 数据已并入 analytics）",
}

// normalizeSettingValue 按 rule.Kind 规范化 1.x value（JSON 编码，可能存在
// 字符串化标量的历史形态："false"），输出 2.0 value 的 JSON 文档。
func normalizeSettingValue(rule settingRule, raw string) (json.RawMessage, error) {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("settings 值非 JSON: %w", err)
	}
	switch rule.Kind {
	case "bool":
		b, err := asBool(v)
		if err != nil {
			return nil, err
		}
		return json.Marshal(b)
	case "int":
		i, err := asInt(v)
		if err != nil {
			return nil, err
		}
		return json.Marshal(i)
	case "enum_contact":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("contact_required 期望字符串，实际 %T", v)
		}
		switch s {
		case "none", "phone", "email", "qq", "any":
			return json.Marshal(s)
		default:
			return nil, fmt.Errorf("require_contact 取值 %q 无 2.0 对应", s)
		}
	default: // string / passthrough：字符串解引号，其余原样
		if s, ok := v.(string); ok {
			return json.Marshal(s)
		}
		return json.Marshal(v)
	}
}

func asBool(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		if b, err := strconv.ParseBool(x); err == nil {
			return b, nil
		}
	case float64:
		return x != 0, nil
	}
	return false, fmt.Errorf("期望布尔值，实际 %T(%v)", v, v)
}

func asInt(v any) (int64, error) {
	switch x := v.(type) {
	case float64:
		return int64(x), nil
	case string:
		if i, err := strconv.ParseInt(x, 10, 64); err == nil {
			return i, nil
		}
	}
	return 0, fmt.Errorf("期望整数，实际 %T(%v)", v, v)
}
