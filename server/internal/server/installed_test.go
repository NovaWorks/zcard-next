package server

// installGuard 三态行为测试：未安装（/ 302、静态放行、/install 200）、
// 已安装（/install 静态提示页、其余直通）。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInstallGuardRedirects(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // 418：若直通则以 418 标记
	})

	t.Run("未安装：/ 302 /install", func(t *testing.T) {
		h := installGuard(func() bool { return false })(next)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/install" {
			t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("未安装：SPA 路由 302 /install", func(t *testing.T) {
		h := installGuard(func() bool { return false })(next)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/", nil))
		if rec.Code != http.StatusFound {
			t.Fatalf("code=%d", rec.Code)
		}
	})

	t.Run("未安装：静态资源放行", func(t *testing.T) {
		h := installGuard(func() bool { return false })(next)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app-x.js", nil))
		if rec.Code != http.StatusTeapot {
			t.Fatalf("静态资源应直通: code=%d", rec.Code)
		}
	})

	t.Run("未安装：/install 200", func(t *testing.T) {
		h := installGuard(func() bool { return false })(next)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/install", nil))
		if rec.Code != http.StatusTeapot {
			t.Fatalf("code=%d", rec.Code)
		}
	})

	t.Run("已安装：/install 静态提示页", func(t *testing.T) {
		h := installGuard(func() bool { return true })(next)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/install", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("code=%d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "系统已安装") {
			t.Fatalf("提示页缺少「系统已安装」: %s", rec.Body.String())
		}
	})

	t.Run("已安装：其余路径直通", func(t *testing.T) {
		h := installGuard(func() bool { return true })(next)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusTeapot {
			t.Fatalf("code=%d", rec.Code)
		}
	})
}
