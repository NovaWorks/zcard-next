package notify

// 通道实现（/）：Email（SMTP）/ Inbox（站内信）。
// 降级纪律（铁律）：SMTP 未配置 → status=skipped 不报错（友商教训）；
// 配置运行时读取——settings 变更不重启生效（1.x MailService 平移）。
// SMS/Telegram 交付（接口位已留，事件矩阵按 enabled 逐通道独立投递）。

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
)

// Channel 通道能力接口（adapter 位；新通道 = 新文件 + 注册，不改核心）。
type Channel interface {
	// Name 通道标识（email/inbox/sms/telegram）。
	Name() string
	// Deliver 投递（返回 skipped 语义错误由调用方落日志；配置缺失不报错）。
	Deliver(ctx context.Context, msg notifyport.Message) error
	// Ready 配置是否齐全（验证码等必须送达场景的前置校验；无配置依赖恒 true）。
	Ready(ctx context.Context) bool
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
	// The admin form writes individual smtp_* keys. A persisted host marks that
	// form as authoritative; clearing it must not reactivate a legacy account.
	raw, err := c.settings.GetJSON(ctx, "notify", "smtp_host")
	if err != nil {
		return nil, err
	}
	cfg := &notifyport.SMTPConfig{Port: 465, Security: "auto", Auth: "auto", TLSVerify: true}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg.Host); err != nil {
			return nil, fmt.Errorf("notify: SMTP 服务器配置格式错误")
		}
		for _, field := range []struct {
			key  string
			dest any
		}{
			{"smtp_security", &cfg.Security}, {"smtp_auth", &cfg.Auth}, {"smtp_tls_verify", &cfg.TLSVerify},
			{"smtp_port", &cfg.Port}, {"smtp_user", &cfg.Username},
			{"smtp_password", &cfg.Password}, {"smtp_from", &cfg.From}, {"smtp_name", &cfg.FromName},
		} {
			raw, err := c.settings.GetJSON(ctx, "notify", field.key)
			if err != nil {
				return nil, err
			}
			if len(raw) > 0 && json.Unmarshal(raw, field.dest) != nil {
				return nil, fmt.Errorf("notify: SMTP 配置格式错误（%s）", field.key)
			}
		}
		cfg.Enabled = strings.TrimSpace(cfg.Host) != ""
	} else {
		raw, err = c.settings.GetJSON(ctx, "notify", "smtp")
		if err != nil || len(raw) == 0 {
			return nil, err
		}
		if err := jsonUnmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("notify: SMTP 配置不合法: %w", err)
		}
	}
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.From = strings.TrimSpace(cfg.From)
	if cfg.From == "" {
		cfg.From = cfg.Username
	}
	if cfg.Security == "" {
		cfg.Security = "auto"
	}
	if cfg.Auth == "" {
		cfg.Auth = "auto"
	}
	return cfg, nil
}

// ErrSkipped 降级哨兵（配置缺失/禁用；落日志 status=skipped，不算失败不重试）。
var ErrSkipped = errors.New("notify: 通道未配置或禁用（skipped）")

// Ready checks the saved configuration before mandatory verification mail.
func (c *EmailChannel) Ready(ctx context.Context) bool {
	cfg, err := c.smtpConfig(ctx)
	return err == nil && cfg != nil && cfg.Enabled && cfg.Host != "" && cfg.Port > 0 && cfg.Port <= 65535 && validMailbox(cfg.From) && validSMTPMode(cfg.Security) && validSMTPAuth(cfg.Auth)
}

func (c *EmailChannel) Deliver(ctx context.Context, msg notifyport.Message) (result error) {
	cfg, err := c.smtpConfig(ctx)
	if err != nil {
		return smtpFailure("configuration", err, cfg)
	}
	if cfg == nil || !cfg.Enabled || cfg.Host == "" {
		return ErrSkipped
	}
	stage := "configuration"
	defer func() {
		if result != nil {
			result = smtpFailure(stage, result, cfg)
		}
	}()
	if !validSMTPMode(cfg.Security) || !validSMTPAuth(cfg.Auth) {
		return fmt.Errorf("SMTP 连接或认证方式无效")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("SMTP 端口须为 1–65535")
	}
	if !validMailbox(cfg.From) {
		return fmt.Errorf("发件邮箱无效，请填写发件邮箱或使用完整邮箱作为 SMTP 用户名")
	}
	if !validMailbox(msg.Recipient) {
		return fmt.Errorf("收件邮箱无效")
	}
	if strings.ContainsAny(msg.Subject+cfg.FromName, "\r\n") {
		return fmt.Errorf("邮件标题和发件人名称不能包含换行")
	}
	from := (&mail.Address{Name: cfg.FromName, Address: cfg.From}).String()
	header := []string{
		"From: " + from,
		"To: " + msg.Recipient,
		"Subject: " + mimeEncode(msg.Subject),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
	}
	body := strings.Join(header, "\r\n") + "\r\n\r\n" + msg.Body
	// Limit the whole SMTP exchange, including a server that accepts TCP but
	// never answers. Request cancellation closes the connection immediately.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	addr := net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port))
	stage = "connect"
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	rawConn := conn
	defer rawConn.Close()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	tlsConfig := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: !cfg.TLSVerify} // Explicit admin choice; verification defaults to true.
	if cfg.Security == "tls" || (cfg.Security == "auto" && cfg.Port == 465) {
		stage = "tls"
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = tlsConn
	}
	stage = "greeting"
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if cfg.Security == "starttls" || (cfg.Security == "auto" && cfg.Port != 465) {
		stage = "starttls"
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				return err
			}
		} else if cfg.Security == "starttls" {
			return fmt.Errorf("服务器未宣告 STARTTLS 支持")
		}
	}
	if cfg.Auth != "none" && (cfg.Username != "" || cfg.Auth != "auto") {
		stage = "auth"
		auth, err := smtpAuthentication(client, cfg)
		if err != nil {
			return err
		}
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	// MAIL FROM uses the bare address; the display name belongs only in headers.
	stage = "from"
	if err := client.Mail(cfg.From); err != nil {
		return err
	}
	stage = "recipient"
	if err := client.Rcpt(msg.Recipient); err != nil {
		return err
	}
	stage = "data"
	w, err := client.Data()
	if err != nil {
		return err
	}
	stage = "body"
	if _, err := w.Write([]byte(body)); err != nil {
		return err
	}
	stage = "accept"
	if err := w.Close(); err != nil {
		return err
	}
	// DATA acceptance means submitted. A QUIT disconnect must not turn it into
	// a failure and invite duplicate sends.
	_ = client.Quit()
	return nil
}

func validMailbox(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Contains(value, "@") && !strings.ContainsAny(value, "\r\n")
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
func (c *InboxChannel) Ready(context.Context) bool { return true }

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
