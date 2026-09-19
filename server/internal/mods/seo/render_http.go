package seo

// Classic crawler rendering uses the same route data as SEO-enabled themes.

import (
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func u64str(v uint64) string { return strconv.FormatUint(v, 10) }

// priceYuan 分 → 元字符串（两位小数）。
func priceYuan(cents int64) string {
	return money.Cents(cents).Format(2)
}

func timeFmt(unix int64) string {
	return time.Unix(unix, 0).UTC().Format("2006-01-02T15:04:05Z")
}

// TryRenderBot 见文件头注释。
func (s *SeoService) TryRenderBot(w http.ResponseWriter, r *http.Request) bool {
	path := strings.TrimRight(r.URL.Path, "/")
	segment := strings.Split(strings.TrimPrefix(path, "/"), "/")[0]
	if _, ok := privateTitles[segment]; !ok && path != "" && path != "/products" && path != "/posts" && path != "/points" && !strings.HasPrefix(path, "/product/") && !strings.HasPrefix(path, "/posts/") {
		return false
	}
	d, status, err := s.pageData(r)
	if err != nil {
		d = noindexPage(s.loadSite(r.Context()), r.Host, path, "页面暂时无法加载")
		status = 503
	}
	output, err := renderPage(d)
	if err != nil {
		http.Error(w, "page rendering failed", 503)
		return true
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if status == 503 {
		w.Header().Set("Retry-After", "60")
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(output))
	}
	return true
}
