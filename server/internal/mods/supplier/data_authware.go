package supplier

// T2 HMAC 四头鉴权中间件（P2-03）：
//   X-Supply-Key / X-Supply-Timestamp / X-Supply-Nonce / X-Supply-Signature
// 流程：四头解析 → key 查账户 → 状态 approved → ±300s 时间窗 → nonce 防重放
// （DB supply_nonces UNIQUE(key,nonce)）→ 双口径验签（常数时间）→ 账户注入 context。
// 挂载：仅 /api/supply/* 路由组（不挂 JWT——架构测试规则 9；Ping 免签名）。
//
// 签名字节不变式：签名哈希的字节 === 实际发出的字节——直接读 *http.Request
// （Method/URL.Path/RawQuery/Body），不依赖 Kratos operation 转写。

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
)

// 鉴权错误码（下游可编程处理）。
const (
	errMissingHeaders  = "missing_headers"
	errUnknownKey      = "unknown_key"
	errAccountDisabled = "account_disabled"
	errTimestampSkew   = "timestamp_skew"
	errNonceReplay     = "nonce_replay"
	errInvalidSig      = "invalid_signature"
)

// 默认时间窗（秒）。
const defaultTimestampSkew = 300

// accountCtxKey 鉴权账户上下文键。
type accountCtxKey struct{}

// SupplyAccountID 从 context 取鉴权账户（服务实现消费）。
func SupplyAccountID(ctx context.Context) uint64 {
	id, _ := ctx.Value(accountCtxKey{}).(uint64)
	return id
}

// AuthStore 鉴权数据访问（中间件依赖；由 SupplierRepoImpl 实现）。
type AuthStore interface {
	// AccountByKey 按 api_key 查账户（返回解密后的 secret）。
	AccountByKey(ctx context.Context, key string) (*ent.SupplierAccount, string, error)
	// ConsumeNonce 消费 nonce（UNIQUE 约束冲突 = 重放）。
	ConsumeNonce(ctx context.Context, key, nonce string, expiresAt time.Time) error
}

// SupplyAuthFilter HMAC 四头鉴权过滤器（http.Handler 级，能拿到原始 *http.Request
// ——Kratos v3 Transporter 不暴露 method/path/body，签名不变式要求原始字节）。
// 仅匹配 /api/supply/*（Ping 免签名）；账户注入 request context（Handler 内读取）。
func SupplyAuthFilter(store AuthStore, skew int64) func(http.Handler) http.Handler {
	if skew <= 0 {
		skew = defaultTimestampSkew
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isSupplyPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			ctx, err := authenticate(r, store, skew)
			if err != nil {
				writeAuthError(w, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// authenticate 执行四头校验（分离便于单测）。
func authenticate(r *http.Request, store AuthStore, skew int64) (context.Context, error) {
	key := r.Header.Get("X-Supply-Key")
	tsStr := r.Header.Get("X-Supply-Timestamp")
	nonce := r.Header.Get("X-Supply-Nonce")
	sig := r.Header.Get("X-Supply-Signature")
	if key == "" || tsStr == "" || nonce == "" || sig == "" {
		return nil, newAuthError(errMissingHeaders, "缺少鉴权头（X-Supply-Key/Timestamp/Nonce/Signature）")
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return nil, newAuthError(errTimestampSkew, "timestamp 非法")
	}
	if abs64(time.Now().Unix()-ts) > skew {
		return nil, newAuthError(errTimestampSkew, "timestamp 超出时间窗（±300s）")
	}
	account, secret, err := store.AccountByKey(r.Context(), key)
	if err != nil {
		return nil, newAuthError(errUnknownKey, "未知 api_key")
	}
	if string(account.Status) != "approved" {
		return nil, newAuthError(errAccountDisabled, "账户未审核或已禁用")
	}
	// 验签（先验签后写 nonce——1.x 教训：未验签请求不污染 nonce 缓存）
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body)) // 恢复 body（后续 Kratos 解码需要）
	if !verifyDual(secret, r.Method, r.URL.Path, r.URL.RawQuery, tsStr, nonce, body, sig) {
		return nil, newAuthError(errInvalidSig, "签名校验失败")
	}
	if err := store.ConsumeNonce(r.Context(), key, nonce, time.Unix(ts, 0).Add(time.Duration(skew)*time.Second)); err != nil {
		return nil, newAuthError(errNonceReplay, "nonce 已使用（重放拒绝）")
	}
	return context.WithValue(r.Context(), accountCtxKey{}, account.ID), nil
}

// isSupplyPath 供货路由判定（Ping 免签名）。
func isSupplyPath(path string) bool {
	if len(path) < len("/api/supply/") || path[:len("/api/supply/")] != "/api/supply/" {
		return false
	}
	return path != "/api/supply/ping"
}

// writeAuthError 401 响应（错误码供下游程序化处理）。
func writeAuthError(w http.ResponseWriter, err error) {
	code := "invalid_signature"
	msg := err.Error()
	var ae *authError
	if errors.As(err, &ae) {
		code = ae.Code
		msg = ae.Message
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"code":401,"reason":"` + code + `","message":"` + msg + `"}`))
}

// authError 带错误码的错误（HTTP 401；code 供下游程序化处理）。
type authError struct {
	Code    string
	Message string
}

func (e *authError) Error() string { return fmt.Sprintf("supplier.auth.%s: %s", e.Code, e.Message) }

func newAuthError(code, msg string) error { return &authError{Code: code, Message: msg} }

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// 编译期断言：AuthStore 由 repo 实现（见 data_repo.go）。
var _ AuthStore = (*SupplierRepoImpl)(nil)
