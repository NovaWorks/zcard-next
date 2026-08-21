package notify

// 腾讯云短信（API 3.0 / sms.tencentcloudapi.com）——零 SDK REST + TC3-HMAC-SHA256 签名。
// 模板变量注意：腾讯云 TemplateParamSet 是位置数组（{1}{2}...）——本通道按变量名
// 字典序排列（code/minutes/site），运营建模板时 {1}{2}{3} 依次对应。
// TC3 签名（腾讯云官方算法）：
//   canonicalRequest = "POST" \n "/" \n "" \n canonicalHeaders \n signedHeaders \n sha256hex(payload)
//   stringToSign     = "TC3-HMAC-SHA256" \n timestamp \n date/sms/tc3_request \n sha256hex(canonicalRequest)
//   SecretDate = HMAC-SHA256("TC3"+SecretKey, date)
//   SecretService = HMAC-SHA256(SecretDate, "sms")
//   SecretSigning = HMAC-SHA256(SecretService, "tc3_request")
//   Signature = hex(HMAC-SHA256(SecretSigning, stringToSign))
// golden vector 见 data_notify_test.go（Python 独立计算固化）。

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	tencentSMSEndpoint = "https://sms.tencentcloudapi.com"
	tencentSMSHost     = "sms.tencentcloudapi.com"
	tencentSMSVersion  = "2021-01-11"
)

// tencentSendSmsPayload SendSms 请求体（API 3.0 JSON）。
type tencentSendSmsPayload struct {
	PhoneNumberSet   []string `json:"PhoneNumberSet"`
	SmsSdkAppId      string   `json:"SmsSdkAppId"`
	SignName         string   `json:"SignName"`
	TemplateId       string   `json:"TemplateId"`
	TemplateParamSet []string `json:"TemplateParamSet,omitempty"`
}

// deliverTencent 腾讯云 SendSms（缺 SDK AppID/模板 ID → skipped）。
func (c *SMSChannel) deliverTencent(ctx context.Context, cfg *SMSConfig, phone string, params map[string]string) error {
	if cfg.TemplateCode == "" || cfg.SdkAppID == "" {
		return ErrSkipped
	}
	payload, err := json.Marshal(tencentSendSmsPayload{
		PhoneNumberSet:   []string{tencentPhone(phone)},
		SmsSdkAppId:      cfg.SdkAppID,
		SignName:         cfg.SignName,
		TemplateId:       cfg.TemplateCode,
		TemplateParamSet: tencentParamSet(params),
	})
	if err != nil {
		return err
	}
	timestamp := time.Now().Unix()
	auth, err := tencentSign(cfg.AccessKey, cfg.AccessSecret, timestamp, payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tencentSMSEndpoint, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Host", tencentSMSHost)
	req.Header.Set("X-TC-Action", "SendSms")
	req.Header.Set("X-TC-Version", tencentSMSVersion)
	req.Header.Set("X-TC-Timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("Authorization", auth)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: SMS 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var out struct {
		Response struct {
			Error *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
			SendStatusSet []struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"SendStatusSet"`
		} `json:"Response"`
	}
	_ = json.Unmarshal(body, &out)
	if out.Response.Error != nil {
		return fmt.Errorf("notify: SMS 发送失败 %s: %s", out.Response.Error.Code, out.Response.Error.Message)
	}
	if len(out.Response.SendStatusSet) > 0 && out.Response.SendStatusSet[0].Code != "Ok" {
		return fmt.Errorf("notify: SMS 发送失败 %s: %s",
			out.Response.SendStatusSet[0].Code, out.Response.SendStatusSet[0].Message)
	}
	return nil
}

// tencentPhone 手机号补国际区号（国内号 +86）。
func tencentPhone(phone string) string {
	if strings.HasPrefix(phone, "+") {
		return phone
	}
	return "+86" + phone
}

// tencentParamSet 模板变量转位置数组（变量名字典序——与模板 {1}{2}{3} 对应）。
func tencentParamSet(params map[string]string) []string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, params[k])
	}
	return out
}

// tencentSign TC3-HMAC-SHA256 签名（Authorization 头）。
func tencentSign(secretID, secretKey string, timestamp int64, payload []byte) (string, error) {
	date := time.Unix(timestamp, 0).UTC().Format("2006-01-02")
	canonicalHeaders := "content-type:application/json; charset=utf-8\nhost:" + tencentSMSHost + "\n"
	signedHeaders := "content-type;host"
	canonicalRequest := "POST\n/\n\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + sha256Hex(payload)
	credentialScope := date + "/sms/tc3_request"
	stringToSign := "TC3-HMAC-SHA256\n" + strconv.FormatInt(timestamp, 10) + "\n" + credentialScope + "\n" + sha256Hex([]byte(canonicalRequest))
	secretDate := hmacSHA256([]byte("TC3"+secretKey), date)
	secretService := hmacSHA256(secretDate, "sms")
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))
	return "TC3-HMAC-SHA256 Credential=" + secretID + "/" + credentialScope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + signature, nil
}

// hmacSHA256 HMAC-SHA256（hex 化在调用方）。
func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// sha256Hex 内容 SHA-256 hex。
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
