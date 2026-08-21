package notify

// 七牛云短信（sms.qiniuapi.com）——零 SDK REST + QBox 签名（与官方 go-sdk 逐字节一致）。
// 签名串（qiniu SignRequest）：
//   data = METHOD path（含 query）
//   + "\nHost: " + host
//   + "\nContent-Type: " + contentType（缺省跳过）
//   + 任意 X-Qiniu-* 头（本通道无）
//   + "\n\n" + body（application/json 请求体原样参与签名）
// Authorization = "Qiniu " + AccessKey + ":" + urlsafe_base64(HMAC-SHA1(SecretKey, data))
// 字段映射：sms_sign = 签名 ID（signature_id）、sms_template_code = 模板 ID（template_id）。
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
	"strings"
)

const (
	qiniuSMSEndpoint = "https://sms.qiniuapi.com/v1/message"
	qiniuSMSHost     = "sms.qiniuapi.com"
)

// qiniuSendMessagePayload 发送短信请求体（官方 MessagesRequest）。
type qiniuSendMessagePayload struct {
	SignatureID string            `json:"signature_id"`
	TemplateID  string            `json:"template_id"`
	Mobiles     []string          `json:"mobiles"`
	Parameters  map[string]string `json:"parameters"`
}

// deliverQiniu 七牛 SendMessage（缺签名 ID/模板 ID → skipped）。
func (c *SMSChannel) deliverQiniu(ctx context.Context, cfg *SMSConfig, phone string, params map[string]string) error {
	if cfg.TemplateCode == "" || cfg.SignName == "" {
		return ErrSkipped
	}
	payload, err := json.Marshal(qiniuSendMessagePayload{
		SignatureID: cfg.SignName,
		TemplateID:  cfg.TemplateCode,
		Mobiles:     []string{phone},
		Parameters:  params,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, qiniuSMSEndpoint, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", qiniuSign(cfg.AccessKey, cfg.AccessSecret, string(payload)))
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: SMS 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("notify: SMS 发送失败 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.JobID == "" {
		return fmt.Errorf("notify: SMS 发送失败（响应异常）: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

// qiniuSign QBox 签名（HMAC-SHA1 + urlsafe base64；golden vector 契约测试目标）。
func qiniuSign(accessKey, accessSecret, body string) string {
	signingStr := "POST /v1/message\nHost: " + qiniuSMSHost +
		"\nContent-Type: application/json\n\n" + body
	mac := hmac.New(sha1.New, []byte(accessSecret))
	mac.Write([]byte(signingStr))
	sign := base64.URLEncoding.EncodeToString(mac.Sum(nil))
	return "Qiniu " + accessKey + ":" + sign
}
