// Package adapter 支付渠道适配器（P1-04 M1b）：alipay / wechat / epay 三渠道。
//
// 渠道能力接口见 payment/port（Provider/CallbackVerifier/Capturer/Refunder）。
// 适配器无状态——渠道凭据经 CreatePaymentRequest.Config / VerifyCallback 的 cfg 逐调用传入，
// 天然支持同一驱动多渠道实例（不同 pid/key）。
//
// 安全纪律（§5.5）：签名串哈希的字节 === 实际发出的字节；凭据与签名永不进日志；
// 验签失败与单号错误对外语义分离（验签失败 401，状态冲突 200，系统错误 500）。
package adapter

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
)

// RegisterAll 注册全部内置渠道 adapter（启动装配期调用一次）。
func RegisterAll(reg port.Registry) error {
	if reg == nil {
		return fmt.Errorf("payment: registry 为 nil")
	}
	reg.Register(NewEpay())
	reg.Register(NewAlipay())
	reg.Register(NewWechat())
	reg.Register(NewEpusdt())
	reg.Register(NewStripe())
	return nil
}

// sortParams 按 key 字典序拼接非空参数为 k=v&k=v...（排除指定 key）。
func sortParams(params map[string]string, excludes ...string) string {
	ex := map[string]bool{}
	for _, k := range excludes {
		ex[k] = true
	}
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if ex[k] || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(params[k])
	}
	return b.String()
}

// md5Hex 小写 hex（epay/wechat MD5 口径）。
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// md5Upper 大写 hex（wechat sign 口径）。
func md5Upper(s string) string {
	return strings.ToUpper(md5Hex(s))
}

// hmacSHA256 HMAC-SHA256。
func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

// hmacSHA256Upper HMAC-SHA256 大写 hex（wechat HMAC-SHA256 口径）。
func hmacSHA256Upper(key, s string) string {
	return strings.ToUpper(hex.EncodeToString(hmacSHA256([]byte(key), []byte(s))))
}

// constantTimeEq 常数时间字符串比较（签名/密码比对防时序侧信道）。
func constantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// centsToYuan 分 → 元字符串（两位小数，纯整数运算，禁止 float 存金额）。
func centsToYuan(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", map[bool]string{true: "-", false: ""}[neg], cents/100, cents%100)
}

// yuanToCents 元字符串 → 分（两位小数，纯整数运算；最多两位小数，超精度拒绝）。
// 用于回调金额回读：渠道回调给「元」，本地核对为「分」。
func yuanToCents(yuan string) (int64, error) {
	yuan = strings.TrimSpace(yuan)
	neg := strings.HasPrefix(yuan, "-")
	yuan = strings.TrimPrefix(yuan, "-")
	parts := strings.SplitN(yuan, ".", 2)
	if len(parts) > 2 {
		return 0, fmt.Errorf("invalid amount %q", yuan)
	}
	var integer, frac int64
	if _, err := fmt.Sscanf(parts[0], "%d", &integer); err != nil {
		return 0, fmt.Errorf("invalid amount %q", yuan)
	}
	if len(parts) == 2 {
		f := parts[1]
		if len(f) > 2 {
			return 0, fmt.Errorf("amount precision > 2: %q", yuan)
		}
		for len(f) < 2 {
			f += "0"
		}
		if _, err := fmt.Sscanf(f, "%d", &frac); err != nil {
			return 0, fmt.Errorf("invalid amount %q", yuan)
		}
	}
	cents := integer*100 + frac
	if neg {
		cents = -cents
	}
	return cents, nil
}

// sha256Sum 供 alipay RSA2 签名/验签使用（签名消息摘要）。
func sha256Sum(data []byte) []byte {
	s := sha256.Sum256(data)
	return s[:]
}

// jsonMust 序列化回调原文（审计用；失败返回空）。
func jsonMust(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(b)
}

// nonceStr 生成随机字母数字串（微信 nonce_str / 幂等键）。
func nonceStr(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	rb := make([]byte, n)
	_, _ = rand.Read(rb)
	for i := range b {
		b[i] = charset[int(rb[i])%len(charset)]
	}
	return string(b)
}
