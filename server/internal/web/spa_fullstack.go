//go:build fullstack

// Package web SPA 服务（fullstack 形态，规划 §10.1）。
//
// 消费 web 锚点包的 DistFS：storefront 兜底根 + admin 独立前缀；
// 未匹配路径回落 index.html（前端路由接管）。缓存纪律（铁律 8）：
// index.html 永不缓存（no-store），hash 资产长缓存 immutable。
package web

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	rootweb "github.com/NovaWorks/zcard-next/server/web"
)

var (
	storefrontFS fs.FS
	adminFS      fs.FS
)

func init() {
	var err error
	if storefrontFS, err = fs.Sub(rootweb.DistFS, "storefront"); err != nil {
		panic("web: storefront 子树缺失")
	}
	if adminFS, err = fs.Sub(rootweb.DistFS, "admin"); err != nil {
		panic("web: admin 子树缺失")
	}
}

// Available 是否 fullstack 形态（server 接线分流：true 才挂 SPA 路由）。
func Available() bool { return true }

// Handler SPA 静态服务。
type Handler struct {
	root       fs.FS
	prefix     string // 挂载前缀（如 /admin；strip 后再查 FS）
	indexBytes []byte
}

// NewStorefrontHandler 前台 SPA（兜底根，无前缀）。
func NewStorefrontHandler() *Handler { return newHandler(storefrontFS, "") }

// NewAdminHandler 管理后台 SPA（挂 /admin 前缀；请求路径剥前缀后查 FS）。
func NewAdminHandler() *Handler { return newHandler(adminFS, "/admin") }

func newHandler(root fs.FS, prefix string) *Handler {
	h := &Handler{root: root, prefix: prefix}
	if b, err := fs.ReadFile(root, "index.html"); err == nil {
		h.indexBytes = b
	}
	return h
}

// ServeHTTP 静态资源优先；未匹配回落 index.html（SPA 前端路由）。
// SSG 产物（vite-ssg）：/product/1 命中 product/1/index.html 静态页（SEO 完整 HTML）；
// 无静态页的路径回落 index.html 由前端路由接管。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	p := r.URL.Path
	if h.prefix != "" {
		p = strings.TrimPrefix(p, h.prefix) // /admin/assets/x → assets/x
	}
	up := strings.TrimPrefix(path.Clean("/"+p), "/")
	// 根（""）走 SPA 回落；清洗后仍含穿越段（理论不可达）拒绝
	if up == "" {
		h.serveIndex(w, r)
		return
	}
	if strings.Contains(up, "../") || up == ".." {
		http.NotFound(w, r)
		return
	}
	if f, err := h.root.Open(up); err == nil {
		st, statErr := f.Stat()
		if statErr == nil && !st.IsDir() {
			defer f.Close()
			// hash 资产内容寻址：长缓存 immutable
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			http.ServeContent(w, r, st.Name(), time.Time{}, f.(io.ReadSeeker))
			return
		}
		_ = f.Close()
	}
	// SSG 静态页（vite-ssg 扁平产物）：路径 + .html（/product/1 → product/1.html）
	if f, err := h.root.Open(up + ".html"); err == nil {
		st, statErr := f.Stat()
		if statErr == nil && !st.IsDir() {
			defer f.Close()
			h.serveStaticHTML(w, r, f, st)
			return
		}
		_ = f.Close()
	}
	h.serveIndex(w, r)
}

// serveStaticHTML SSG 静态页：短缓存（内容随发布更新）。
func (h *Handler) serveStaticHTML(w http.ResponseWriter, r *http.Request, f fs.File, st fs.FileInfo) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, st.Name(), st.ModTime(), f.(io.ReadSeeker))
}

// serveIndex SPA 回落：index.html 永不缓存（新版本发布即生效）。
func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	if h.indexBytes == nil {
		http.Error(w, "index.html missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(h.indexBytes)
}
