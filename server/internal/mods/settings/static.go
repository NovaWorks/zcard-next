package settings

// 模板目录静态服务（主题预览图访问）：/templates/<key>/<file>
// 只读 + 图片白名单扩展名 + 防路径穿越（与 media 静态服务同纪律）。

import (
	"net/http"
	"path"
	"path/filepath"
	"strings"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

// templateExtWhitelist 可暴露的扩展名（主题预览图；theme.json 等清单不直接外露）。
var templateExtWhitelist = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
	".svg":  "image/svg+xml",
}

// RegisterTemplateStatic 注册模板目录静态服务（SPA 兜底前调用）。
func RegisterTemplateStatic(mux *khttp.Server) {
	mux.HandlePrefix("/templates/", http.HandlerFunc(serveTemplateStatic))
}

func serveTemplateStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, "/templates/")
	if rel == "" || strings.HasSuffix(rel, "/") {
		http.NotFound(w, r) // 目录列表禁用
		return
	}
	ext := strings.ToLower(path.Ext(rel))
	if _, ok := templateExtWhitelist[ext]; !ok {
		http.NotFound(w, r) // 非白名单扩展名一律 404
		return
	}
	full := filepath.Join(templatesRoot, filepath.FromSlash(path.Clean(rel)))
	// 防穿越：Clean 后必须仍在模板根内
	if !strings.HasPrefix(full, filepath.Clean(templatesRoot)+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, full)
}
