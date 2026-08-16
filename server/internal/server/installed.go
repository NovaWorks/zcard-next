package server

// EnsureInstalled 中间件（P0-04 T2）：未安装时除 /install、/health、静态资源外一律 302 /install。
// i18n 中间件：Accept-Language 解析 → i18n.WithLocale（默认语言 settings.i18n，当前 zh_CN）。

import (
	"context"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/platform/i18n"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

// ensureInstalled 构造（installed 判定函数由 settings 注入，避免 server 反向依赖 mods 仓储）。
func ensureInstalled(isInstalled func() bool) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if !isInstalled() {
				if tr, ok := transport.FromServerContext(ctx); ok {
					if hc, ok := tr.(khttp.Context); ok {
						p := hc.Request().URL.Path
						// 豁免：安装页/健康检查/静态
						if p == "/install" || p == "/health" || !strings.HasPrefix(p, "/api/") {
							return handler(ctx, req)
						}
						hc.Response().Header().Set("Location", "/install")
						hc.Response().WriteHeader(302)
						return nil, nil
					}
				}
			}
			return handler(ctx, req)
		}
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
