package notify

// 通道实现（/）：Email（SMTP）/ Inbox（站内信）。
// 降级纪律（铁律）：SMTP 未配置 → status=skipped 不报错（友商教训）；
// 配置运行时读取——settings 变更不重启生效（1.x MailService 平移）。
// SMS/Telegram 交付（接口位已留，事件矩阵按 enabled 逐通道独立投递）。

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/smtp"
	"strings"

	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
)

// Channel 通道能力接口（adapter 位；新通道 = 新文件 + 注册，不改核心）。
type Channel interface {
	// Name 通道标识（email/inbox/sms/telegram）。
	Name() string
	// Deliver 投递（返回 skipped 语义错误由调用方落日志；配置缺失不报错）。
	Deliver(ctx context.Context, msg notifyport.Message) error
}

// ── Email（SMTP）──────────────────────────────────────────

// EmailChannel SMTP 邮件通道。
type EmailChannel struct {
	settings notifyport.SettingsReader
}

// NewEmailChannel 构造。
func NewEmailChannel(settings notifyport.SettingsReader) *EmailChannel {
	return &EmailChannel{settings: settings}
}

func (*EmailChannel) Name() string { return "email" }

// smtpConfig 运行时读配置（变更不重启）。
func (c *EmailChannel) smtpConfig(ctx context.Context) (*notifyport.SMTPConfig, error) {
	raw, err := c.settings.GetJSON(ctx, "notify", "smtp")
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil // 未配置 → 降级
	}
	var cfg notifyport.SMTPConfig
	if err := jsonUnmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("notify: SMTP 配置不合法: %w", err)
	}
	return &cfg, nil
}

// ErrSkipped 降级哨兵（配置缺失/禁用；落日志 status=skipped，不算失败不重试）。
var ErrSkipped = errors.New("notify: 通道未配置或禁用（skipped）")

// Deliver 发送邮件。
func (c *EmailChannel) Deliver(ctx context.Context, msg notifyport.Message) error {
	cfg, err := c.smtpConfig(ctx)
	if err != nil {
		return err
	}
	if cfg == nil || !cfg.Enabled || cfg.Host == "" {
		return ErrSkipped
	}
	if msg.Recipient == "" || !strings.Contains(msg.Recipient, "@") {
		return fmt.Errorf("notify: 收件邮箱无效 %q", msg.Recipient)
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	from := cfg.From
	if cfg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", cfg.FromName, cfg.From)
	}
	header := make([]string, 0, 5)
	header = append(header,
		"From: "+from,
		"To: "+msg.Recipient,
		"Subject: "+mimeEncode(msg.Subject),
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
	)
	body := strings.Join(header, "\r\n") + "\r\n\r\n" + msg.Body

	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}
	// TLS：465 隐式；587/25 走明文+STARTTLS（服务器支持时客户端自动升级由 net/smtp 处理）
	if cfg.Port == 465 {
		return dialTLS(addr, cfg.Host, auth, from, msg.Recipient, body)
	}
	return smtp.SendMail(addr, auth, from, []string{msg.Recipient}, []byte(body))
}

func dialTLS(addr, host string, auth smtp.Auth, from, to, body string) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return fmt.Errorf("notify: SMTP TLS 连接失败: %w", err)
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("notify: SMTP 客户端构造失败: %w", err)
	}
	defer client.Close()
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("notify: SMTP 认证失败: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(body)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// mimeEncode 邮件主题编码（非 ASCII → RFC 2047 B 编码）。
func mimeEncode(s string) string {
	for _, r := range s {
		if r > 127 {
			return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
		}
	}
	return s
}

// ── Inbox（站内信）────────────────────────────────────────

// InboxChannel 站内信通道（写 notifications 表；铃铛 API 读取）。
type InboxChannel struct {
	repo *NotifyRepo
}

// NewInboxChannel 构造。
func NewInboxChannel(repo *NotifyRepo) *InboxChannel {
	return &InboxChannel{repo: repo}
}

func (*InboxChannel) Name() string { return "inbox" }

// Deliver 写站内信（userID=0 → skipped）。
func (c *InboxChannel) Deliver(ctx context.Context, msg notifyport.Message) error {
	if msg.UserID == 0 {
		return ErrSkipped
	}
	return c.repo.CreateInbox(ctx, msg.UserID, msg.Subject, plainText(msg.Body), msg.EventType, msg.BizID)
}

// plainText HTML → 纯文本（站内信正文不带标签）。
func plainText(html string) string {
	var b strings.Builder
	skip := false
	for _, r := range html {
		switch {
		case r == '<':
			skip = true
		case r == '>':
			skip = false
		case !skip:
			b.WriteRune(r)
		}
	}
	return b.String()
}
