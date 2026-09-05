package authn

// TOTP 两步验证（）：密钥 AES-GCM 加密存储（ZCARD_DATA_KEY），
// 验证窗口 ±1（30s 步长）。启用流程：生成密钥 → 管理员扫码/手输 → 验证一次确认。

import (
	"fmt"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTPIssuer 服务名（ authenticator app 显示）。
const TOTPIssuer = "ZCard"

// GenerateTOTP 生成新密钥（返回 otpauth URL 供二维码 + 密钥明文供手输；由调用方加密存储）。
func GenerateTOTP(account string) (secret string, url string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      TOTPIssuer,
		AccountName: account,
		Period:      30,
		Digits:      otp.DigitsSix,
	})
	if err != nil {
		return "", "", fmt.Errorf("authn: 生成 TOTP 密钥失败: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

// VerifyTOTP 验证六位动态码（±1 窗口）。
func VerifyTOTP(secret, code string) bool {
	if secret == "" || len(code) != 6 {
		return false
	}
	return totp.Validate(code, secret)
}
