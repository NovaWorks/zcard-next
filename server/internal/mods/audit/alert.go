package audit

// T5 告警阈值接线（P2-06 收尾）：异常检测阈值（settings.security）→
// 安全事件计数超阈值时经 notify 管理员通道告警（telegram/email）。
//
// 阈值项（settings security 组 alert 子键；缺省用默认值）：
//   fetch_fail_per_ip   同 IP 取货失败次数/窗口（默认 10/10min）
//   login_fail_per_ip   同 IP 登录失败（默认 20/10min）
//   decrypt_per_admin   管理员解密频率（默认 30/10min）
// 去重窗口：同 key 告警 30min 内只发一次（告警风暴防护）。
//
// 埋点接入：Security() 内部顺带计数——业务方零改动。

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
)

// AlertConfig 告警阈值配置（settings security 组 alert 键）。
type AlertConfig struct {
	Enabled bool `json:"enabled"`
	// 阈值（次/10 分钟窗口）
	FetchFailPerIP  int `json:"fetch_fail_per_ip"`
	LoginFailPerIP  int `json:"login_fail_per_ip"`
	DecryptPerAdmin int `json:"decrypt_per_admin"`
	// 告警通道（notify 通道名，如 telegram/email）
	Channel   string `json:"channel"`
	Recipient string `json:"recipient"` // telegram chat_id / 管理员邮箱
}

// 默认阈值（配置缺省回退）。
func defaultAlertConfig() AlertConfig {
	return AlertConfig{
		Enabled:         true,
		FetchFailPerIP:  10,
		LoginFailPerIP:  20,
		DecryptPerAdmin: 30,
		Channel:         "telegram",
	}
}

// alertWindow 计数窗口。
const alertWindow = 10 * time.Minute

// alertDedup 同 key 告警去重窗口。
const alertDedup = 30 * time.Minute

// Alerter 告警器（阈值计数 + 去重 + 通道投递）。
type Alerter struct {
	settings notifyport.SettingsReader
	sender   notifyport.Sender

	mu        sync.Mutex
	counts    map[string][]time.Time // key → 窗口内事件时间
	lastAlert map[string]time.Time   // key → 上次告警时间
}

// NewAlerter 构造。
func NewAlerter(settings notifyport.SettingsReader, sender notifyport.Sender) *Alerter {
	return &Alerter{
		settings:  settings,
		sender:    sender,
		counts:    map[string][]time.Time{},
		lastAlert: map[string]time.Time{},
	}
}

// alertKey 事件计数键（阈值判定的维度）。
func alertKey(action, ip string) string {
	return action + "|" + ip
}

// Count 计数并判定：达到阈值 → 触发告警（去重窗口内只发一次）。
// 业务方调用点：Security() 内部（fetch 失败/登录失败/解密）。
func (a *Alerter) Count(ctx context.Context, action, ip string, subject, body string) {
	if a == nil {
		return
	}
	cfg := a.config(ctx)
	if !cfg.Enabled {
		return
	}
	threshold := a.thresholdOf(cfg, action)
	if threshold <= 0 {
		return // 该维度未配置阈值（不计数不告警）
	}

	key := alertKey(action, ip)
	now := time.Now()

	a.mu.Lock()
	// 滑动窗口计数
	win := a.counts[key]
	kept := win[:0]
	for _, t := range win {
		if now.Sub(t) < alertWindow {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	a.counts[key] = kept
	count := len(kept)
	// 去重窗口
	if last, ok := a.lastAlert[key]; ok && now.Sub(last) < alertDedup {
		a.mu.Unlock()
		return
	}
	if count < threshold {
		a.mu.Unlock()
		return
	}
	a.lastAlert[key] = now
	// 达到阈值后重置计数（下一窗口重新累计）
	a.counts[key] = nil
	a.mu.Unlock()

	if a.sender == nil {
		return
	}
	_ = a.sender.Send(ctx, notifyport.Message{
		EventType: "security.alert", Channel: cfg.Channel,
		Recipient: cfg.Recipient, Locale: "zh_CN",
		Subject: subject,
		Body:    body,
		Variables: map[string]string{
			"action": action, "ip": ip, "count": itoa(count), "window": "10m",
		},
	})
}

// config 运行时读阈值配置（缺省回退默认值）。
func (a *Alerter) config(ctx context.Context) AlertConfig {
	cfg := defaultAlertConfig()
	if a.settings == nil {
		return cfg
	}
	raw, err := a.settings.GetJSON(ctx, "security", "alert")
	if err != nil || len(raw) == 0 {
		return cfg
	}
	// 宽松解析：零值字段保留默认
	parsed := cfg
	_ = jsonUnmarshalAlert(raw, &parsed)
	if parsed.FetchFailPerIP == 0 {
		parsed.FetchFailPerIP = cfg.FetchFailPerIP
	}
	if parsed.LoginFailPerIP == 0 {
		parsed.LoginFailPerIP = cfg.LoginFailPerIP
	}
	if parsed.DecryptPerAdmin == 0 {
		parsed.DecryptPerAdmin = cfg.DecryptPerAdmin
	}
	if parsed.Channel == "" {
		parsed.Channel = cfg.Channel
	}
	return parsed
}

// thresholdOf 按事件名取阈值（未覆盖的维度返回 0 = 不计数）。
func (a *Alerter) thresholdOf(cfg AlertConfig, action string) int {
	switch {
	case containsStr(action, "fetch"):
		return cfg.FetchFailPerIP
	case containsStr(action, "login_failed"):
		return cfg.LoginFailPerIP
	case containsStr(action, "decrypt"), containsStr(action, "view_content"):
		return cfg.DecryptPerAdmin
	}
	return 0
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOfStr(s, sub) >= 0)
}

func indexOfStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// jsonUnmarshalAlert 告警配置解析（宽松：失败保留默认值）。
func jsonUnmarshalAlert(raw []byte, v *AlertConfig) error {
	return json.Unmarshal(raw, v)
}
