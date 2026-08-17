// Package port 为 notify 模块对外契约（零依赖包）。
package port

import "context"

// Message 通知消息（通道无关）。
type Message struct {
	EventType string // 事件类型（order.paid 等）
	Channel   string // email | inbox | sms | telegram
	Recipient string // 收件人（邮箱/用户ID字符串/手机号/chat_id）
	Locale    string // zh_CN 默认
	Subject   string
	Body      string
	// Variables 白名单变量（渲染后随日志留档）
	Variables map[string]string
	// BizRef 业务引用（notification_logs.biz_type/biz_id）
	BizType string
	BizID   uint64
	// UserID 站内信目标（inbox 通道）
	UserID uint64
}

// Sender 通知发送端口（业务模块消费，通道 A）。
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// Brand 站点品牌（白标解析结果）。
type Brand struct {
	SiteName string
	Logo     string
}

// BrandResolver 站点品牌解析（reseller 实现；分站订单按 subsite_id 解析白标）。
// fail-closed：解析不到返回 ok=false——调用方不得回退主站品牌（铁律：分站邮件
// 绝不暴露主站品牌）。
type BrandResolver interface {
	ResolveBrand(ctx context.Context, subsiteID uint64) (brand Brand, ok bool)
}

// SettingsReader 通道配置读取（notify 模块消费 settings，通道 A）。
// SMTP 等通道运行时读取——配置变更不重启生效（1.x MailService 纪律）。
type SettingsReader interface {
	// GetJSON 读配置组键（不存在返回 nil, nil——调用方走降级）。
	GetJSON(ctx context.Context, group, key string) ([]byte, error)
}

// SMTPConfig SMTP 通道配置（settings notify 组）。
type SMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	FromName string `json:"from_name"`
	Enabled  bool   `json:"enabled"`
}
