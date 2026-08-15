package crypto

// 密码哈希：bcrypt（成本 12）。
// 登录密码与取货查询密码统一走本包，比对使用库内 constant-time 实现（铁律 12）。

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

// HashPassword 哈希密码（bcrypt）。
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("crypto: hash password: %w", err)
	}
	return string(b), nil
}

// VerifyPassword constant-time 校验。错误信息不区分「用户不存在」与「密码错误」（防枚举）。
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
