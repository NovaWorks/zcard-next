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
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
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

// SupplyAuthware HMAC 四头中间件工厂。
func SupplyAuthware(store AuthStore, skew int64) middleware.Middleware {
	if skew <= 0 {
		skew = defaultTimestampSkew
	}
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return nil, errors.New("supplier: 非服务端上下文")
			}
			hc, ok := tr.(khttp.Context)
			if !ok {
				return nil, errors.New("supplier: 仅支持 HTTP 传输")
			}
			r := hc.Request()
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
			account, secret, err := store.AccountByKey(ctx, key)
			if err != nil {
				return nil, newAuthError(errUnknownKey, "未知 api_key")
			}
			if string(account.Status) != "approved" {
				return nil, newAuthError(errAccountDisabled, "账户未审核或已禁用")
			}
			// 验签（先验签后写 nonce——1.x 教训：未验签请求不污染 nonce 缓存）
			body, _ := io.ReadAll(r.Body)
			if !verifyDual(secret, r.Method, r.URL.Path, r.URL.RawQuery, tsStr, nonce, body, sig) {
				return nil, newAuthError(errInvalidSig, "签名校验失败")
			}
			if err := store.ConsumeNonce(ctx, key, nonce, time.Unix(ts, 0).Add(time.Duration(skew)*time.Second)); err != nil {
				return nil, newAuthError(errNonceReplay, "nonce 已使用（重放拒绝）")
			}
			return handler(context.WithValue(ctx, accountCtxKey{}, account.ID), req)
		}
	}
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
