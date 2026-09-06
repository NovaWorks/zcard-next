package notify

// Telegram bot 通道（ 收尾）：sendMessage HTTP API
// （友商 telegram/notify/botapi 协议知识参照；配置运行时读取）。
//
// 端点：POST https://api.telegram.org/bot{token}/sendMessage
// 载荷：{"chat_id": "...", "text": "...", "parse_mode": "HTML"}
// 出站经 httpx 安全客户端（SSRF 防护）；未配置 → skipped 降级。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/httpx"
)

// TelegramConfig bot 配置（settings notify 组 telegram 键）。
type TelegramConfig struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"bot_token"`
	// ChatIDs 管理员群/频道（逗号分隔；告警与群发多目标）
	ChatIDs string `json:"chat_ids"`
}

// TelegramChannel Telegram bot 通道。
type TelegramChannel struct {
	settings notifyport.SettingsReader
	client   *http.Client
}

// NewTelegramChannel 构造。
func NewTelegramChannel(settings notifyport.SettingsReader) *TelegramChannel {
	return &TelegramChannel{settings: settings, client: httpx.NewSafeClient(15 * time.Second)}
}

func (*TelegramChannel) Name() string { return "telegram" }

// tgConfig 运行时读配置。
func (c *TelegramChannel) tgConfig(ctx context.Context) (*TelegramConfig, error) {
	raw, err := c.settings.GetJSON(ctx, "notify", "telegram")
	if err != nil || len(raw) == 0 {
		return nil, nil
	}
	var cfg TelegramConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("notify: Telegram 配置不合法: %w", err)
	}
	return &cfg, nil
}

// Deliver 发送消息（Recipient = chat_id；空则发配置的全部 chat_ids）。
func (c *TelegramChannel) Ready(ctx context.Context) bool {
	cfg, err := c.tgConfig(ctx)
	return err == nil && cfg != nil && cfg.Enabled && cfg.BotToken != ""
}

func (c *TelegramChannel) Deliver(ctx context.Context, msg notifyport.Message) error {
	cfg, err := c.tgConfig(ctx)
	if err != nil {
		return err
	}
	if cfg == nil || !cfg.Enabled || cfg.BotToken == "" {
		return ErrSkipped
	}
	targets := []string{}
	if id := trimSpace(msg.Recipient); id != "" {
		targets = append(targets, id)
	} else {
		for _, id := range splitComma(cfg.ChatIDs) {
			targets = append(targets, id)
		}
	}
	if len(targets) == 0 {
		return ErrSkipped
	}
	var lastErr error
	for _, chatID := range targets {
		if err := c.sendOne(ctx, cfg.BotToken, chatID, msg.Subject+"\n"+msg.Body); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// sendOne 单目标发送（HTML parse mode；Subject+Body 合并文本）。
func (c *TelegramChannel) sendOne(ctx context.Context, token, chatID, text string) error {
	payload, err := json.Marshal(map[string]string{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: Telegram 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notify: Telegram 发送失败 %d: %s", resp.StatusCode, truncateStr(string(body), 200))
	}
	return nil
}

func splitComma(s string) []string {
	var out []string
	for _, p := range bytes.Split([]byte(s), []byte(",")) {
		if v := trimSpace(string(p)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n') {
		s = s[:len(s)-1]
	}
	return s
}

func truncateStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
