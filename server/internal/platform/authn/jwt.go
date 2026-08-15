// Package authn 双 realm JWT（规划 §7.3）。
//
// admin 与 user 独立密钥与声明（防提权串用）：admin 令牌在 user 接口无效，反之亦然。
// access 2h；refresh 14d 轮换（sessions 表，M3）。本包为纯 JWT 原语（golang-jwt/v5），
// 不 import Kratos transport（架构测试规则 3：platform 纯净）。
package authn

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Realm 令牌领域。
type Realm string

// 双 realm。
const (
	RealmAdmin Realm = "admin"
	RealmUser  Realm = "user"
)

// Claims 自定义声明。
type Claims struct {
	Subject   uint64 `json:"sub"`            // admin_users.id / users.id
	Username  string `json:"username"`       // 登录名快照
	RoleID    uint64 `json:"role,omitempty"` // admin realm：角色 ID
	Realm     Realm  `json:"realm"`          // admin | user（校验时必须匹配）
	TokenType string `json:"typ"`            // access | refresh
	jwt.RegisteredClaims
}

// ErrInvalidToken 令牌无效/过期/领域不匹配（对外统一 401，不区分原因防探测）。
var ErrInvalidToken = errors.New("authn: 令牌无效")

// Signer 签发器（每 realm 一把密钥，从 conf.Security 装配）。
type Signer struct {
	adminKey []byte
	userKey  []byte
	ttl      time.Duration
}

// NewSigner 构造。密钥长度 >= 32 字节。
func NewSigner(adminKey, userKey []byte, accessTTL time.Duration) (*Signer, error) {
	if len(adminKey) < 32 || len(userKey) < 32 {
		return nil, fmt.Errorf("authn: JWT 密钥长度不足（需 >= 32 字节，admin=%d user=%d）", len(adminKey), len(userKey))
	}
	if accessTTL <= 0 {
		accessTTL = 2 * time.Hour
	}
	return &Signer{adminKey: adminKey, userKey: userKey, ttl: accessTTL}, nil
}

// AccessTTL 访问令牌有效期。
func (s *Signer) AccessTTL() time.Duration { return s.ttl }

// Issue 签发 access 令牌。
func (s *Signer) Issue(realm Realm, subject uint64, username string, roleID uint64) (token string, expiresAt time.Time, err error) {
	now := time.Now()
	expiresAt = now.Add(s.ttl)
	claims := &Claims{
		Subject:   subject,
		Username:  username,
		RoleID:    roleID,
		Realm:     realm,
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			Issuer:    "zcard",
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	key := s.adminKey
	if realm == RealmUser {
		key = s.userKey
	}
	token, err = t.SignedString(key)
	return token, expiresAt, err
}

// Verify 校验令牌并返回声明；realm 必须与期望一致（双 realm 防串用）。
func (s *Signer) Verify(realm Realm, tokenString string) (*Claims, error) {
	key := s.adminKey
	if realm == RealmUser {
		key = s.userKey
	}
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("authn: 非预期签名算法 %v", t.Header["alg"])
		}
		return key, nil
	})
	if err != nil {
		return nil, ErrInvalidToken
	}
	if claims.Realm != realm || claims.TokenType != "access" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
