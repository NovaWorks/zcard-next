package notify

// SMS 通道（ 收尾）：阿里云/腾讯云/七牛三通道——零 SDK REST。
// 配置运行时读取 settings notify 组扁平键（后台「邮件短信」页保存）：
// sms_provider / sms_key / sms_secret / sms_sign / sms_template_code / sms_sdk_app_id
// 兼容旧版 notify.sms JSON blob（access_key 等；存量部署无缝迁移），
// 未配置/缺凭据 → skipped 降级。
//
// 消息体 Body = 模板变量 JSON 键值对象（如 {"code":"123456","minutes":"5"}），
// 各通道按自身模板变量规范转换（阿里云/七牛键值对象；腾讯云按变量名排序转数组）。
//
// 阿里云签名算法（RPC 风格）：
// 1. 参数去 Signature 后 ksort
// 2. RFC 3986 变体编码（+ → %20）
// 3. stringToSign = "POST" & enc("/") & enc(canonicalQuery)
// 4. sig = base64(HMAC-SHA1(stringToSign, accessSecret + "&"))
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

// SMSConfig 短信通道配置（settings notify 组扁平键）。
type SMSConfig struct {
	Provider     string // aliyun | tencent | qiniu
	AccessKey    string
	AccessSecret string
	SignName     string // 签名（阿里云/腾讯云：签名名称；七牛：签名 ID）
	TemplateCode string // 模板 ID
	SdkAppID     string // 腾讯云专属（SmsSdkAppId）
}

// SMSChannel 短信通道（provider 分发）。
type SMSChannel struct {
	settings notifyport.SettingsReader
	client   *http.Client // httpx 安全客户端（SSRF 防护）
}

// NewSMSChannel 构造。
func NewSMSChannel(settings notifyport.SettingsReader) *SMSChannel {
	return &SMSChannel{settings: settings, client: httpx.NewSafeClient(10 * time.Second)}
}

func (*SMSChannel) Name() string { return "sms" }

// smsConfig 运行时读配置（变更不重启；扁平键缺失 → 回退旧 notify.sms JSON blob）。
func (c *SMSChannel) smsConfig(ctx context.Context) (*SMSConfig, error) {
	readStr := func(group, key string) string {
		raw, err := c.settings.GetJSON(ctx, group, key)
		if err != nil || len(raw) == 0 || string(raw) == "null" {
			return ""
		}
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return ""
		}
		return strings.TrimSpace(v)
	}
	cfg := &SMSConfig{
		Provider:     readStr("notify", "sms_provider"),
		AccessKey:    readStr("notify", "sms_key"),
		AccessSecret: readStr("notify", "sms_secret"),
		SignName:     readStr("notify", "sms_sign"),
		TemplateCode: readStr("notify", "sms_template_code"),
		SdkAppID:     readStr("notify", "sms_sdk_app_id"),
	}
	if cfg.Provider == "" {
		cfg.Provider = "aliyun"
	}
	// 扁平键未配置 → 兼容旧版 notify.sms 复合键（阿里云口径；enabled 显式开关）
	if cfg.AccessKey == "" || cfg.AccessSecret == "" {
		raw, err := c.settings.GetJSON(ctx, "notify", "sms")
		if err == nil && len(raw) > 0 && string(raw) != "null" {
			var legacy struct {
				Enabled      bool   `json:"enabled"`
				AccessKey    string `json:"access_key"`
				AccessSecret string `json:"access_secret"`
				SignName     string `json:"sign_name"`
				TemplateCode string `json:"template_code"`
			}
			if json.Unmarshal(raw, &legacy) == nil && legacy.Enabled && legacy.AccessKey != "" {
				cfg.Provider = "aliyun"
				cfg.AccessKey = legacy.AccessKey
				cfg.AccessSecret = legacy.AccessSecret
				if cfg.SignName == "" {
					cfg.SignName = legacy.SignName
				}
				if cfg.TemplateCode == "" {
					cfg.TemplateCode = legacy.TemplateCode
				}
			}
		}
	}
	return cfg, nil
}

// Deliver 发送短信（Recipient = 手机号；Body = 模板变量 JSON 键值对象）。
// Ready 短信凭据齐全（Deliver 的 ErrSkipped 同源判定）。
func (c *SMSChannel) Ready(ctx context.Context) bool {
	cfg, err := c.smsConfig(ctx)
	return err == nil && cfg != nil && cfg.AccessKey != "" && cfg.AccessSecret != ""
}

func (c *SMSChannel) Deliver(ctx context.Context, msg notifyport.Message) error {
	cfg, err := c.smsConfig(ctx)
	if err != nil {
		return err
	}
	if cfg.AccessKey == "" || cfg.AccessSecret == "" {
		return ErrSkipped
	}
	phone := strings.TrimSpace(msg.Recipient)
	if len(phone) != 11 || !strings.HasPrefix(phone, "1") {
		return fmt.Errorf("notify: 手机号无效 %q", phone)
	}
	params, err := parseSmsParams(msg.Body)
	if err != nil {
		return err
	}
	switch cfg.Provider {
	case "tencent":
		return c.deliverTencent(ctx, cfg, phone, params)
	case "qiniu":
		return c.deliverQiniu(ctx, cfg, phone, params)
	default:
		return c.deliverAliyun(ctx, cfg, phone, params)
	}
}

// parseSmsParams 模板变量解析（Body 必须为 JSON 键值对象；非法 → 空参数继续走通道）。
func parseSmsParams(body string) (map[string]string, error) {
	out := map[string]string{}
	body = strings.TrimSpace(body)
	if body == "" {
		return out, nil
	}
	if !json.Valid([]byte(body)) {
		return out, nil
	}
	_ = json.Unmarshal([]byte(body), &out)
	return out, nil
}

// deliverAliyun 阿里云 Dysmsapi SendSms（TemplateParam 键值对象）。
func (c *SMSChannel) deliverAliyun(ctx context.Context, cfg *SMSConfig, phone string, params map[string]string) error {
	if cfg.TemplateCode == "" {
		return ErrSkipped
	}
	tplParam, _ := json.Marshal(params)

	reqParams := map[string]string{
		"PhoneNumbers":     phone,
		"SignName":         cfg.SignName,
		"TemplateCode":     cfg.TemplateCode,
		"TemplateParam":    string(tplParam),
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
	reqParams["Signature"] = aliyunSign(reqParams, cfg.AccessSecret)

	form := url.Values{}
	for k, v := range reqParams {
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
