package audit

// 操作审计过滤器（）：变更类 admin 操作（POST/PUT/DELETE）自动落 audit_logs。
//
// 实现为 http.Filter（非 Kratos middleware）——中间件层的 Transporter 是内部
// *khttp.Transport 类型，拿不到原始 *http.Request（method/path/body）；
// Filter 层直接持有请求与响应状态（supplier HMAC 同款模式）。
//
// before/after 快照：由各模块注册的「路由前缀 → 取值函数」注册表提供；
// 未注册的路由仍记录操作元数据（无快照）。敏感字段脱敏（凭据永不入审计）。
// 纪律：审计写失败不阻断业务（异步落库；失败仅日志——1.x 纪律）。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
)

// snapshotFn 快照取值函数（聚合键 → 变更前数据；由业务模块注册）。
type snapshotFn func(ctx context.Context, r *http.Request) (before map[string]any)

// snapshotRegistry 路由前缀 → 快照函数（各模块 service 声明自己的审计聚合键）。
var snapshotRegistry = map[string]snapshotFn{}

// RegisterSnapshot 注册快照函数（routePrefix 如 /api/v1/admin/settings）。
func RegisterSnapshot(routePrefix string, fn snapshotFn) {
	snapshotRegistry[routePrefix] = fn
}

// OpAuditFilter 操作审计过滤器工厂。
// signer：admin JWT 校验器（操作者身份取自令牌——鉴权中间件注入的 claims 只
// 存在于下游 handler ctx，不会写回 Filter 层的 *http.Request，故此处自行校验；
// HMAC 校验代价可忽略）。
// permOf：operation → 权限点（authz 目录查询注入，避免循环依赖）。
// 响应状态：仅 2xx 记录（错误响应未产生变更）。
func OpAuditFilter(repo *AuditRepo, signer *authn.Signer, permOf func(op string) (code string, ok bool), opOf func(r *http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 只审计变更类管理面操作（GET 不落）
			if (r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodDelete) ||
				!strings.HasPrefix(r.URL.Path, "/api/v1/admin/") {
				next.ServeHTTP(w, r)
				return
			}
			// 请求载荷读取（body 恢复供后续解码）
			bodyAfter := ReadBodyJSON(r)

			rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
			next.ServeHTTP(rec, r)

			// 非 2xx：未产生变更不落审计
			if rec.code < 200 || rec.code >= 300 {
				return
			}
			in := OpLogInput{
				OperatorType: "admin",
				Action:       r.Method,
				Route:        r.URL.Path,
				IP:           clientIP(r),
				UserAgent:    truncate(r.UserAgent(), 255),
			}
			// 操作者：admin JWT 直接校验（见函数注释）；失败留 0 = 未知
			if signer != nil {
				if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
					if claims, err := signer.Verify(authn.RealmAdmin, strings.TrimSpace(tok)); err == nil {
						in.OperatorID = claims.Subject
					}
				}
			}
			if op := opOf(r); op != "" {
				if code, ok := permOf(op); ok {
					in.PermissionPoint = code
				}
			}
			// before：注册表命中时查库取旧值语义（各模块声明聚合键）
			if fn, ok := matchSnapshot(r.URL.Path); ok {
				in.Before = fn(r.Context(), r)
			}
			in.After = bodyAfter

			// 异步落库（repo 内部吞错——审计失败不影响业务）
			go repo.WriteOpLog(context.WithoutCancel(r.Context()), in)
		})
	}
}

// statusRecorder 捕获响应状态码。
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// matchSnapshot 路由前缀匹配快照函数（最长前缀优先）。
func matchSnapshot(path string) (snapshotFn, bool) {
	best := ""
	var fn snapshotFn
	for prefix, f := range snapshotRegistry {
		if strings.HasPrefix(path, prefix) && len(prefix) > len(best) {
			best, fn = prefix, f
		}
	}
	return fn, best != ""
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
	}
	return truncate(r.RemoteAddr, 64)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ReadBodyJSON 读请求体为 map（body 恢复供后续解码；敏感字段脱敏）。
func ReadBodyJSON(r *http.Request) map[string]any {
	if r.Body == nil {
		return nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	if len(body) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil
	}
	return redact(m)
}

// redact 敏感字段脱敏（凭据/密码/密钥永不入审计快照）。
func redact(m map[string]any) map[string]any {
	sensitive := map[string]bool{
		"password": true, "secret": true, "credentials": true, "api_secret": true,
		"app_key": true, "config_json": true, "new_secret": true, "private_key": true,
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sensitive[strings.ToLower(k)] {
			out[k] = "****"
			continue
		}
		out[k] = v
	}
	return out
}
