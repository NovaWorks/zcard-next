package notify

// SMS 通道（P2-05 T3 收尾）：阿里云 Dysmsapi——零 SDK REST + HMAC-SHA1 签名
// （1.x SmsService.php 协议知识平移；配置运行时读取，未配置 → skipped 降级）。
//
// 签名算法（阿里云 RPC 风格）：
//   1. 参数去 Signature 后 ksort
//   2. RFC 3986 变体编码（+ → %20）
//   3. stringToSign = "POST" & enc("/") & enc(canonicalQuery)
//   4. sig = base64(HMAC-SHA1(stringToSign, accessSecret + "&"))
// golden vector 见 data_notify_test.go（Python 独立计算固化）。

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/httpx"
)

// aliyunEndpoint Dysmsapi 地址。
const aliyunEndpoint = "https://dysmsapi.aliyuncs.com/"

// SMSConfig 阿里云 SMS 配置（settings notify 组 sms 键）。
type SMSConfig struct {
	Enabled      bool   `json:"enabled"`
	AccessKey    string `json:"access_key"`
	AccessSecret string `json:"access_secret"`
	SignName     string `json:"sign_name"`
	TemplateCode string `json:"template_code"` // 验证码/通用模板
}

// SMSChannel 阿里云短信通道。
type SMSChannel struct {
	settings notifyport.SettingsReader
	client   *http.Client // httpx 安全客户端（SSRF 防护）
}

// NewSMSChannel 构造。
func NewSMSChannel(settings notifyport.SettingsReader) *SMSChannel {
	return &SMSChannel{settings: settings, client: httpx.NewSafeClient(10 * time.Second)}
}

func (*SMSChannel) Name() string { return "sms" }

// smsConfig 运行时读配置（变更不重启）。
func (c *SMSChannel) smsConfig(ctx context.Context) (*SMSConfig, error) {
	raw, err := c.settings.GetJSON(ctx, "notify", "sms")
	if err != nil || len(raw) == 0 {
		return nil, nil
	}
	var cfg SMSConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("notify: SMS 配置不合法: %w", err)
	}
	return &cfg, nil
}

// Deliver 发送短信（Recipient = 手机号；Body = 模板变量 JSON）。
func (c *SMSChannel) Deliver(ctx context.Context, msg notifyport.Message) error {
	cfg, err := c.smsConfig(ctx)
	if err != nil {
		return err
	}
	if cfg == nil || !cfg.Enabled || cfg.AccessKey == "" || cfg.TemplateCode == "" {
		return ErrSkipped
	}
	phone := strings.TrimSpace(msg.Recipient)
	if len(phone) != 11 || !strings.HasPrefix(phone, "1") {
		return fmt.Errorf("notify: 手机号无效 %q", phone)
	}
	tplParam := strings.TrimSpace(msg.Body)
	if tplParam == "" || !json.Valid([]byte(tplParam)) {
		tplParam = "{}"
	}

	params := map[string]string{
		"PhoneNumbers":     phone,
		"SignName":         cfg.SignName,
		"TemplateCode":     cfg.TemplateCode,
		"TemplateParam":    tplParam,
		"AccessKeyId":      cfg.AccessKey,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"SignatureNonce":   fmt.Sprintf("zcard-%d", time.Now().UnixNano()),
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"Format":           "JSON",
		"Action":           "SendSms",
		"Version":          "2017-05-25",
		"RegionId":         "cn-hangzhou",
	}
	params["Signature"] = aliyunSign(params, cfg.AccessSecret)

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, aliyunEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: SMS 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	_ = json.Unmarshal(body, &out)
	if out.Code != "OK" {
		return fmt.Errorf("notify: SMS 发送失败 %s: %s", out.Code, out.Message)
	}
	return nil
}

// aliyunSign 阿里云 RPC 签名（HMAC-SHA1 + Base64；golden vector 契约测试目标）。
func aliyunSign(params map[string]string, accessSecret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "Signature" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, rfc3986Encode(k)+"="+rfc3986Encode(params[k]))
	}
	canonical := strings.Join(pairs, "&")
	stringToSign := "POST&" + rfc3986Encode("/") + "&" + rfc3986Encode(canonical)
	mac := hmac.New(sha1.New, []byte(accessSecret+"&"))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// rfc3986Encode URL 编码（Go QueryEscape 的 + 空格 → %20；-_.~ 保留）。
func rfc3986Encode(s string) string {
	encoded := url.QueryEscape(s)
	return strings.ReplaceAll(encoded, "+", "%20")
}
