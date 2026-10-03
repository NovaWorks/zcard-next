package orderaccess

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/mail"
	"regexp"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/platform/i18n"
	"github.com/go-kratos/kratos/v3/errors"
)

var requestKeyPattern = regexp.MustCompile(`^(?:[0-9a-fA-F]{32}|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-4[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12})$`)

func Required(ctx context.Context) error {
	return errors.NotFound("order.ACCESS_REQUIRED", i18n.Message(ctx, "order.access_required", "订单访问凭证无效，请使用保存的访问链接或通过下单邮箱验证"))
}

// SecureRequestKey accepts the 128-bit random formats emitted by the storefront.
func SecureRequestKey(value string) bool { return requestKeyPattern.MatchString(value) }

func EmailAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	a, err := mail.ParseAddress(value)
	if err != nil || a.Address != value || len(value) > 255 || !strings.Contains(value, ".") {
		return "", fmt.Errorf("invalid email")
	}
	return strings.ToLower(value), nil
}

// NewToken creates an order-scoped bearer credential with 256 bits of entropy.
// The raw value belongs only in the buyer's session or an authenticated recovery reply.
func NewToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// TokenHash binds the credential to both the storefront tenant and order number.
func TokenHash(tenant uint64, no, token string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("order-access:v1:%d:%s:%s", tenant, no, token)))
	return hex.EncodeToString(sum[:])
}

func VerifyToken(hash string, tenant uint64, no, token string) bool {
	if len(token) != 43 || len(hash) != 64 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hash), []byte(TokenHash(tenant, no, token))) == 1
}
