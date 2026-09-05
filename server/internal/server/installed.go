package server

// EnsureInstalled 中间件（）：未安装时除 /install、/health、静态资源外一律 302 /install。
// i18n 中间件：Accept-Language 解析 → i18n.WithLocale（默认语言 settings.i18n，当前 zh_CN）。

import (
	"context"
	"net/http"
	"path"
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
				// 安装页 / 健康检查 / 安装 API：放行
				if p == "/install" || p == "/health" || strings.HasPrefix(p, "/api/v1/admin/install") {
					next.ServeHTTP(w, r)
					return
				}
				// 业务 API：302 安装向导
				if strings.HasPrefix(p, "/api/") {
					w.Header().Set("Location", "/install")
					w.WriteHeader(http.StatusFound)
					return
				}
				// 非 API：静态资源放行（安装页要加载 JS/CSS/字体）；
				// 其余（/ 首页、SPA 路由、/admin/ 后台等）一律 302 /install——
				// 客户访问首页即见安装入口，无需知道 /install 路径
				if isStaticAsset(p) {
					next.ServeHTTP(w, r)
					return
				}
				w.Header().Set("Location", "/install")
				w.WriteHeader(http.StatusFound)
				return
			}
			// 已安装：/install 返回静态提示页（不依赖前端 JS——SPA 未加载
			// 也能看到提示）；其余路径直通
			if r.URL.Path == "/install" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(installedPage))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// installedPage 已安装提示页（服务端直出静态页；样式与新项目 UI 一致）。
const installedPage = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>系统已安装</title>
<style>
  body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
    background:linear-gradient(160deg,#eff6ff 0%,#f8fafc 60%,#eef2ff 100%);
    font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",Arial,"PingFang SC","Microsoft YaHei",sans-serif;padding:24px}
  .card{width:100%;max-width:420px;background:#fff;border-radius:18px;box-shadow:0 24px 64px rgba(15,23,42,.12);
    padding:36px 32px;text-align:center;box-sizing:border-box}
  .icon{font-size:44px;margin-bottom:10px}
  h1{font-size:20px;font-weight:800;color:#0f172a;margin:0 0 8px}
  p{font-size:14px;color:#64748b;line-height:1.7;margin:0 0 22px}
  .links{display:flex;gap:10px;justify-content:center}
  a{display:inline-block;text-decoration:none;font-size:14px;font-weight:600;
    border-radius:10px;padding:10px 20px;transition:background .12s}
  a.primary{background:#2563eb;color:#fff}
  a.primary:hover{background:#1d4ed8}
  a.secondary{border:1px solid #e2e8f0;color:#334155}
  a.secondary:hover{background:#f8fafc}
  details{margin-top:22px;text-align:left;border:1px solid #e2e8f0;border-radius:10px;overflow:hidden}
  summary{cursor:pointer;font-size:13.5px;font-weight:700;color:#334155;padding:10px 14px;background:#f8fafc;list-style:none}
  summary::before{content:"⚙ "}
  details[open] summary{border-bottom:1px solid #e2e8f0}
  details ol{margin:0;padding:12px 14px 12px 34px;font-size:13px;color:#475569;line-height:1.9}
  code{background:#f1f5f9;border-radius:4px;padding:1px 6px;font-size:12px}
  .warn{color:#b45309;font-size:12.5px;padding:0 14px 12px;margin:0}
</style>
</head>
<body>
  <div class="card">
    <div class="icon">🔒</div>
    <h1>系统已安装</h1>
    <p>安装已完成，本页为安装向导入口，无需重复安装。</p>
    <div class="links">
      <a class="primary" href="/">进入前台</a>
      <a class="secondary" href="/admin/">后台管理</a>
    </div>
    <details>
      <summary>如何重新安装？</summary>
      <p class="warn">⚠️ 重新安装会清空当前全部数据（商品/订单/用户），请先备份数据库！</p>
      <ol>
        <li><b>SQLite</b>：删除数据库文件 <code>data/zcard.db</code></li>
        <li><b>PostgreSQL / MySQL</b>：清空数据库（重建或执行 <code>DROP DATABASE</code>；安装标记在库内，仅删配置不会回到未安装）</li>
        <li>若曾用在线向导切换过数据库：删除 <code>configs/database.yaml</code> 与 <code>data/.install-pending.json</code></li>
        <li>重启服务后访问 <code>/install</code> 即回到安装向导</li>
      </ol>
    </details>
  </div>
</body>
</html>`

// isStaticAsset 静态资源判定：/assets/ 目录（vite 产物）或常见资源扩展名放行；
// .html 视为 SPA 页面（vite-ssg 扁平页），未安装时也跳安装向导。
func isStaticAsset(p string) bool {
	if strings.HasPrefix(p, "/assets/") {
		return true
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".js", ".css", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico",
		".webp", ".woff", ".woff2", ".ttf", ".eot", ".map", ".txt", ".xml", ".json":
		return true
	}
	return false
}

// i18nMiddleware Accept-Language → locale（前缀匹配 zh/en；回落默认 zh_CN；DB 覆盖层 ）。
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
