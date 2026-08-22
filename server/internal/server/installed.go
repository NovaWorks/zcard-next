package server

// EnsureInstalled 中间件（P0-04 T2）：未安装时除 /install、/health、静态资源外一律 302 /install。
// i18n 中间件：Accept-Language 解析 → i18n.WithLocale（默认语言 settings.i18n，当前 zh_CN）。

import (
	"context"
	"net/http"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/platform/i18n"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
)

// installGuard 未安装守门（Filter 层——直接操作 ResponseWriter；
// middleware 返回 nil reply 会被生成的 handler 类型断言 panic，不可用）。
// 未安装时业务 API 302 /install（Web 安装向导）；豁免：安装页/健康检查/
// 安装 API/非 API 静态资源。已安装直通（ops.installed_at 点查）。
func installGuard(isInstalled func() bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isInstalled() {
				p := r.URL.Path
				if p == "/install" || p == "/health" || strings.HasPrefix(p, "/api/v1/admin/install") || !strings.HasPrefix(p, "/api/") {
					next.ServeHTTP(w, r)
					return
				}
				w.Header().Set("Location", "/install")
				w.WriteHeader(http.StatusFound)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// i18nMiddleware Accept-Language → locale（前缀匹配 zh/en；回落默认 zh_CN；DB 覆盖层 M3）。
func i18nMiddleware(defaultLocale string) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			locale := i18n.Locale(defaultLocale)
			if tr, ok := transport.FromServerContext(ctx); ok {
				if al := tr.RequestHeader().Get("Accept-Language"); al != "" {
					tag := strings.ToLower(strings.TrimSpace(strings.Split(al, ",")[0]))
					tag = strings.SplitN(tag, "-", 2)[0]
					switch tag {
					case "zh":
						locale = i18n.ZhCN
					case "en":
						locale = i18n.En
					}
				}
			}
			return handler(i18n.WithLocale(ctx, locale), req)
		}
	}
}
