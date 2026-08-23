package seo

// TryRenderBot 爬虫动态渲染入口（实现 web.BotRenderer 接口）：
// 命中 /product/{id} 或 /posts/{slug} 时向 w 写完整响应（含真 404）并返回 true；
// 其余路径返回 false 交回正常链路。UA 识别在 web 层完成，本方法只负责渲染。

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func u64str(v uint64) string { return strconv.FormatUint(v, 10) }

// priceYuan 分 → 元字符串（两位小数）。
func priceYuan(cents int64) string {
	return strconv.FormatFloat(float64(cents)/100, 'f', 2, 64)
}

func timeFmt(unix int64) string {
	return time.Unix(unix, 0).UTC().Format("2006-01-02T15:04:05Z")
}

const botPageCache = "public, max-age=300"

// TryRenderBot 见文件头注释。
func (s *SeoService) TryRenderBot(w http.ResponseWriter, r *http.Request) bool {
	p := strings.TrimSuffix(r.URL.Path, "/")
	var (
		html     string
		found    bool
		renderErr error
	)
	switch {
	case strings.HasPrefix(p, "/product/"):
		id, err := strconv.ParseUint(strings.TrimPrefix(p, "/product/"), 10, 64)
		if err != nil || id == 0 {
			return false // 非数字 id：交回 SPA（前端路由容错）
		}
		html, found, renderErr = s.renderProduct(r.Context(), r.Host, id)
	case strings.HasPrefix(p, "/posts/"):
		slug := strings.TrimPrefix(p, "/posts/")
		if slug == "" || strings.Contains(slug, "/") {
			return false
		}
		html, found, renderErr = s.renderPost(r.Context(), r.Host, slug)
	default:
		return false
	}
	if renderErr != nil {
		return false // 渲染异常：交回 SPA 兜底（不向爬虫暴露错误页）
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !found {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(notFoundHTML))
		return true
	}
	w.Header().Set("Cache-Control", botPageCache)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(html))
	}
	return true
}

func (s *SeoService) renderProduct(ctx context.Context, host string, id uint64) (string, bool, error) {
	p, err := s.repo.GetProductSEO(ctx, id)
	if err != nil {
		return "", false, err // DB 异常：交回 SPA 兜底
	}
	if p == nil {
		return "", false, nil // 不存在/未上架：真 404
	}
	html, err := renderPage(productPageData(s.loadSite(ctx), host, p))
	return html, true, err
}

func (s *SeoService) renderPost(ctx context.Context, host, slug string) (string, bool, error) {
	p, err := s.repo.GetPostSEO(ctx, slug)
	if err != nil {
		return "", false, err
	}
	if p == nil {
		return "", false, nil
	}
	html, err := renderPage(postPageData(s.loadSite(ctx), host, p))
	return html, true, err
}

const notFoundHTML = `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><title>404 - 页面不存在</title></head><body style="font-family:system-ui,sans-serif;text-align:center;padding:60px 16px"><h1>404</h1><p>页面不存在或已下架。</p></body></html>`
