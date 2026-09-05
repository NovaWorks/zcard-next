// Package license 订阅许可证校验引擎：
//
//	ed25519 签名验证（公钥编译进核心、私钥离线持有）→ 绑定实例 ID/域名
//	→ 订阅到期 → 特性清单（features 控制高级功能开关）。
//
// 离线可验、无网络依赖；篡改/过期/绑定不符一律 fail-closed。
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

// ErrInvalidLicense 许可证非法（签名/格式/绑定任一失败）。
var ErrInvalidLicense = errors.New("license: 许可证无效")

// License 许可证内容（JSON 明文 + 独立签名段）。
type License struct {
	InstanceID string   `json:"instance_id"` // 绑定实例 ID（安装时生成）
	Domain     string   `json:"domain"`      // 绑定主站域名
	Features   []string `json:"features"`    // 授权特性清单
	IssuedAt   string   `json:"issued_at"`   // RFC3339
	ExpiresAt  string   `json:"expires_at"`  // RFC3339（空 = 永久）
}

// SignedLicense 许可证文件结构（<内容 JSON> + <base64 ed25519 签名>）。
type SignedLicense struct {
	License   License `json:"license"`
	Signature string  `json:"signature"` // base64(ed25519.Sign(priv, content))
}

// Verify 校验并解析许可证（fail-closed：任何一项不符即拒绝）。
// pub 为编译进核心的公钥；instanceID/domain 为当前实例绑定值。
func Verify(raw []byte, pub ed25519.PublicKey, instanceID, domain string, now time.Time) (*License, error) {
	var sl SignedLicense
	if err := json.Unmarshal(raw, &sl); err != nil {
		return nil, ErrInvalidLicense
	}
	// 重编码内容段作为签名原文（防字段注入：签名覆盖整个 License JSON）
	content, err := json.Marshal(sl.License)
	if err != nil {
		return nil, ErrInvalidLicense
	}
	sig, err := base64.StdEncoding.DecodeString(sl.Signature)
	if err != nil {
		return nil, ErrInvalidLicense
	}
	if !ed25519.Verify(pub, content, sig) {
		return nil, ErrInvalidLicense
	}
	lic := &sl.License
	// 绑定校验
	if lic.InstanceID != instanceID {
		return nil, ErrInvalidLicense
	}
	if lic.Domain != "" && domain != "" && lic.Domain != domain {
		return nil, ErrInvalidLicense
	}
	// 到期校验（空 = 永久）
	if lic.ExpiresAt != "" {
		exp, err := time.Parse(time.RFC3339, lic.ExpiresAt)
		if err != nil {
			return nil, ErrInvalidLicense
		}
		if now.After(exp) {
			return nil, ErrInvalidLicense
		}
	}
	return lic, nil
}

// HasFeature 特性是否授权（永久版含全部特性可用通配 *）。
func (l *License) HasFeature(feature string) bool {
	if l == nil {
		return false
	}
	for _, f := range l.Features {
		if f == "*" || f == feature {
			return true
		}
	}
	return false
}

// GenerateKeyPair 生成 ed25519 密钥对（发行侧/测试用；私钥离线保管）。
func GenerateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, nil, err
	}
	return pub, priv, nil
}

// Sign 签发（私钥 + License → 文件内容；发行侧使用）。
func Sign(priv ed25519.PrivateKey, lic License) ([]byte, error) {
	content, err := json.Marshal(lic)
	if err != nil {
		return nil, err
	}
	sig := ed25519.Sign(priv, content)
	return json.Marshal(SignedLicense{
		License:   lic,
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
}
