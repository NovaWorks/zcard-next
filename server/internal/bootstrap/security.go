package bootstrap

// 密钥装配：四把钥匙解耦（§4.11.6），env/secret 注入优先，conf 兜底，
// 开发环境允许随机临时密钥（带显著告警；重启失效，仅限本机开发）。

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"

	"github.com/google/wire"
)

var securityProviderSet = wire.NewSet(
	NewSigner,
	NewCardCipher,
)

// resolveKey 密钥解析：env > conf > dev 随机（告警）。
// kind 用于日志标识（jwt_admin/jwt_user/card/data）。
func resolveKey(envName, confVal, kind string) []byte {
	if v := os.Getenv(envName); v != "" {
		return []byte(v)
	}
	if confVal != "" {
		return []byte(confVal)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("bootstrap: 开发随机密钥生成失败：" + err.Error())
	}
	slog.Warn("bootstrap: 未配置密钥，使用随机临时密钥（仅限开发；生产必须经 env 注入）",
		"key", kind, "env", envName)
	return buf
}

func key32(b []byte) []byte {
	if len(b) >= 32 {
		return b[:32]
	}
	out := make([]byte, 32)
	copy(out, b)
	return out
}

// NewSigner 构造 JWT 签发器（admin/user 双 realm 独立密钥）。
func NewSigner(sec *conf.Security) (*authn.Signer, error) {
	if sec == nil {
		sec = &conf.Security{}
	}
	return authn.NewSigner(
		key32(resolveKey("ZCARD_JWT_ADMIN_KEY", sec.JwtAdminKey, "jwt_admin")),
		key32(resolveKey("ZCARD_JWT_USER_KEY", sec.JwtUserKey, "jwt_user")),
		0, // access TTL 默认 2h
	)
}

// NewCardCipher 构造卡密加密器（ZCARD_CARD_KEY：hex 32 字节 → AES-256-GCM + keyed hash）。
func NewCardCipher(sec *conf.Security) (*inventory.CardCipher, error) {
	if sec == nil {
		sec = &conf.Security{}
	}
	raw := os.Getenv("ZCARD_CARD_KEY")
	if raw == "" {
		raw = sec.CardKey
	}
	if raw == "" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		raw = hex.EncodeToString(buf)
		slog.Warn("bootstrap: 未配置 ZCARD_CARD_KEY，使用随机临时卡密密钥（仅限开发；重启后已存卡密不可解）")
	}
	key, err := crypto.ParseHexKey(raw)
	if err != nil {
		return nil, err
	}
	return inventory.NewCardCipher(key)
}
