package audit

// 操作审计中间件（P2-06 T2）：变更类 admin 操作（POST/PUT/DELETE）自动落 audit_logs。
// before/after 快照：由各模块注册的「聚合键取值函数」提供（快照注册表）；
// 未注册的路由仍记录操作元数据（无快照）。
// 纪律：审计写失败不阻断业务（异步落库；无 Redis 同步写但失败仅日志）。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"

	"github.com/go-kratos/kratos/v3/log"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

// snapshotFn 快照取值函数（聚合键 → 变更前/后数据；由业务模块注册）。
type snapshotFn func(ctx context.Context, r *http.Request) (before, after map[string]any)

// snapshotRegistry 路由前缀 → 快照函数（各模块 service 声明自己的审计聚合键）。
var snapshotRegistry = map[string]snapshotFn{}

// RegisterSnapshot 注册快照函数（routePrefix 如 /api/v1/admin/settings）。
func RegisterSnapshot(routePrefix string, fn snapshotFn) {
	snapshotRegistry[routePrefix] = fn
}

// OpAuditMiddleware 操作审计中间件工厂。
// 权限点从 authz 目录（middleware 查询已注入 claims；此处取 operator 元数据）。
func OpAuditMiddleware(repo *AuditRepo, permOf func(op string) (code string, ok bool)) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return handler(ctx, req)
			}
			hc, okHTTP := tr.(khttp.Context)
			if !okHTTP {
				return handler(ctx, req)
			}
			r := hc.Request()
			// 只审计变更类操作（GET 不落）
			if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodDelete {
				return handler(ctx, req)
			}
			// 只审计管理面
			if !strings.HasPrefix(r.URL.Path, "/api/v1/admin/") {
				return handler(ctx, req)
			}
			claims := identity.ClaimsFromContext(ctx)
			resp, err := handler(ctx, req)
			// 业务失败（5xx/错）不落审计（未产生变更）——err 非空跳过
			if err == nil {
				in := OpLogInput{
					OperatorType: "admin",
					Action:       r.Method,
					Route:        r.URL.Path,
					IP:           clientIP(r),
					UserAgent:    truncate(r.UserAgent(), 255),
				}
				if claims != nil {
					in.OperatorID = claims.Subject
				}
				if code, okp := permOf(tr.Operation()); okp {
					in.PermissionPoint = code
				}
				// before/after 快照（注册表命中才采集）
				if fn, okf := matchSnapshot(r.URL.Path); okf {
					before, after := fn(ctx, r)
					in.Before, in.After = before, after
				}
				// 异步落库（失败不影响响应——repo 内部已吞错）
				go repo.WriteOpLog(context.WithoutCancel(ctx), in)
			}
			return resp, err
		}
	}
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

// ReadBodyJSON 读请求体为 map（快照采集用；body 需恢复）。
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
	// 恢复 body 供后续 Kratos 解码（中间件先于 handler 读）
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

var _ = log.Default()
