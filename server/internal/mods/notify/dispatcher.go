package notify

// 事件订阅分发器（）：
// outbox 事件 → 路由表（事件 → 通道集合）→ 模板渲染（白名单变量 + HTML escape）
// → 逐通道投递（独立 enabled；skipped 降级不报错）→ 每条落 notification_logs。
//
// 幂等：processed_events(event_id, consumer) 由 Dispatcher 统一兜底。
// 白名单变量：模板只允许引用 Variables 里登记的键；未知 {{.xxx}} 渲染为空（防注入：
// 变量值 HTML escape；模板本身后台可编辑——只开放占位符子集，禁用 tpl 全功能）。

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"

	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

// Dispatcher 事件分发器。
type Dispatcher struct {
	repo     *NotifyRepo
	channels map[string]Channel       // name → channel
	brand    notifyport.BrandResolver // 白标（nil = 未装配，跳过品牌注入）
}

// NewDispatcher 构造（注册通道）。
func NewDispatcher(repo *NotifyRepo, channels ...Channel) *Dispatcher {
	m := make(map[string]Channel, len(channels))
	for _, c := range channels {
		m[c.Name()] = c
	}
	return &Dispatcher{repo: repo, channels: m}
}

// WithBrandResolver 装配白标解析（wire 经 bootstrap 注入；分站订单邮件品牌隔离）。
func (d *Dispatcher) WithBrandResolver(r notifyport.BrandResolver) *Dispatcher {
	d.brand = r
	return d
}

// 事件 → 默认通道矩阵（模板可覆盖；通道 enabled 逐个独立判定）。
var defaultChannels = map[string][]string{
	"order.paid":         {"email", "inbox"},
	"order.delivered":    {"email", "inbox"},
	"order.completed":    {"inbox"},
	"order.canceled":     {"email", "inbox"},
	"order.refunded":     {"email", "inbox"},
	"payment.failed":     {"inbox"},
	"recharge.succeeded": {"email", "inbox"},
	"user.registered":    {"email", "inbox"},
	// ：工单通知（用户侧新回复；客服侧新工单经 telegram 管理员通道）
	"ticket.created": {"inbox"},
	"ticket.replied": {"email", "inbox"},
}

// HandleEvent outbox 消费入口（Dispatcher Register）。
func (d *Dispatcher) HandleEvent(ctx context.Context, env events.Envelope) error {
	channels := defaultChannels[env.Type]
	if len(channels) == 0 {
		return nil // 无订阅事件（管理员告警类由业务模块显式 Send）
	}
	var payload map[string]any
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return fmt.Errorf("notify: 解析 %s 载荷失败: %w", env.Type, err)
	}
	// 白名单变量（事件载荷扁平化；值统一字符串化 + HTML escape 在渲染期）
	vars := FlattenVars(payload)
	// 品牌隔离 fail-closed：分站上下文邮件注入分站白标；
	// 无白标 → 品牌变量留空（绝不回退主站品牌）
	if env.SubsiteID > 0 && d.brand != nil {
		if brand, ok := d.brand.ResolveBrand(ctx, env.SubsiteID); ok {
			vars["site_name"] = brand.SiteName
			if brand.Logo != "" {
				vars["site_logo"] = brand.Logo
			}
		} else {
			vars["site_name"] = ""
			vars["site_logo"] = ""
		}
	}
	locale := "zh_CN"

	for _, ch := range channels {
		channel, ok := d.channels[ch]
		if !ok {
			continue // 通道未注册（SMS/Telegram ）
		}
		// 模板（事件 × 通道 × 语言；无模板 → 该通道跳过——不算错误）
		tpl, err := d.repo.Template(ctx, env.Type, ch, locale)
		if err != nil {
			continue
		}
		msg := notifyport.Message{
			EventType: env.Type,
			Channel:   ch,
			Locale:    locale,
			Subject:   RenderTemplate(tpl.SubjectTpl, vars),
			Body:      RenderTemplate(tpl.BodyTpl, vars),
			Variables: vars,
		}
		// 收件人解析（email → 载荷 email 字段；inbox → user_id）
		msg.Recipient = vars["email"]
		msg.UserID = parseUint(vars["user_id"])
		msg.BizType = "order"
		msg.BizID = parseUint64(vars["order_id"])

		if err := channel.Deliver(ctx, msg); err != nil {
			if err == ErrSkipped {
				_ = d.repo.WriteLog(ctx, logOf(msg, "skipped", "通道未配置"))
				continue // 降级：不算失败不重试
			}
			_ = d.repo.WriteLog(ctx, logOf(msg, "failed", err.Error()))
			continue // 单通道失败不阻断其余（重试由管理员日志重发）
		}
		_ = d.repo.WriteLog(ctx, logOf(msg, "sent", ""))
	}
	return nil
}

// FlattenVars 载荷扁平化为白名单变量（嵌套 key 以 . 连接；值字符串化）。
func FlattenVars(payload map[string]any) map[string]string {
	out := make(map[string]string)
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, vv := range x {
				key := k
				if prefix != "" {
					key = prefix + "." + k
				}
				walk(key, vv)
			}
		case []any:
			// 数组不展开（变量粒度到对象字段；数组场景业务侧显式拼接进字符串字段）
		case string:
			out[prefix] = x
		case float64:
			out[prefix] = trimFloat(x)
		case bool:
			out[prefix] = fmt.Sprintf("%v", x)
		case nil:
			out[prefix] = ""
		default:
			out[prefix] = fmt.Sprintf("%v", x)
		}
	}
	for k, v := range payload {
		walk(k, v)
	}
	return out
}

// placeholderRe 占位符子集：{{.key}}（仅字母数字下划线点；无函数/管道——防注入）。
var placeholderRe = regexp.MustCompile(`{{\s*\.([A-Za-z0-9_.]+)\s*}}`)

// RenderTemplate 渲染模板：白名单变量替换 + 值 HTML escape（防注入双保险）。
// 未知变量渲染为空字符串。
func RenderTemplate(tpl string, vars map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(tpl, func(m string) string {
		key := placeholderRe.FindStringSubmatch(m)[1]
		if v, ok := vars[key]; ok {
			return html.EscapeString(v)
		}
		return ""
	})
}

// ValidateTemplate 后台模板编辑校验：占位符必须在白名单内（预览前拦截）。
func ValidateTemplate(tpl string, allowed []string) error {
	allowedSet := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allowedSet[a] = true
	}
	for _, m := range placeholderRe.FindAllStringSubmatch(tpl, -1) {
		if !allowedSet[m[1]] {
			return fmt.Errorf("notify.TPL_VAR_NOT_ALLOWED: %q 不在白名单变量内", m[1])
		}
	}
	return nil
}

func logOf(msg notifyport.Message, status, errMsg string) LogInput {
	// 敏感类型（验证码邮件）：正文/变量不落日志——库内无明文纪律
	// （邮件照发，仅审计留档脱敏；BizType 契约见调用方）
	body, vars := msg.Body, msg.Variables
	if msg.BizType == "password_reset" {
		body = "[验证码邮件：内容不落日志]"
		vars = map[string]string{"masked": "true"}
	}
	return LogInput{
		EventType: msg.EventType, BizType: msg.BizType, BizID: msg.BizID,
		Channel: msg.Channel, Recipient: msg.Recipient, Locale: msg.Locale,
		Subject: msg.Subject, Body: body, Status: status,
		ErrorMessage: errMsg, Variables: vars,
	}
}

func parseUint(s string) uint64 { return parseUint64(s) }

func parseUint64(s string) uint64 {
	var v uint64
	_, _ = fmt.Sscanf(strings.TrimSpace(s), "%d", &v)
	return v
}

func trimFloat(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%v", f)
}

// Send 显式发送（管理员告警等业务直调；实现 notifyport.Sender）。
func (d *Dispatcher) Send(ctx context.Context, msg notifyport.Message) error {
	channel, ok := d.channels[msg.Channel]
	if !ok {
		return fmt.Errorf("notify: 未知通道 %q", msg.Channel)
	}
	if err := channel.Deliver(ctx, msg); err != nil {
		if err == ErrSkipped {
			_ = d.repo.WriteLog(ctx, logOf(msg, "skipped", "通道未配置"))
			return nil
		}
		_ = d.repo.WriteLog(ctx, logOf(msg, "failed", err.Error()))
		return err
	}
	_ = d.repo.WriteLog(ctx, logOf(msg, "sent", ""))
	return nil
}

// SubscribedEvents 订阅事件清单（main.go 注册用；与 defaultChannels 键一致）。
func SubscribedEvents() []string {
	out := make([]string, 0, len(defaultChannels))
	for k := range defaultChannels {
		out = append(out, k)
	}
	return out
}
