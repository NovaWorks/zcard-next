package theme

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

var contentTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8",
	".json": "application/json", ".webmanifest": "application/manifest+json", ".txt": "text/plain; charset=utf-8", ".xml": "application/xml", ".wasm": "application/wasm",
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif", ".svg": "image/svg+xml", ".ico": "image/x-icon", ".avif": "image/avif", ".bmp": "image/bmp",
	".woff": "font/woff", ".woff2": "font/woff2", ".ttf": "font/ttf", ".otf": "font/otf", ".eot": "application/vnd.ms-fontobject",
}

func allowedFile(n string) bool { _, ok := contentTypes[strings.ToLower(path.Ext(n))]; return ok }
func imageFile(n string) bool {
	return strings.HasPrefix(contentTypes[strings.ToLower(path.Ext(n))], "image/")
}

// ServePage returns false before writing if the theme cannot be read; callers
// can safely fall back to the embedded Classic page.
func ServePage(w http.ResponseWriter, r *http.Request, t *Theme) bool {
	root, err := os.OpenRoot(t.Dir)
	if err != nil {
		return false
	}
	defer root.Close()
	b, err := root.ReadFile("index.html")
	if err != nil {
		return false
	}
	pos, err := indexHeadEnd(b)
	if err != nil {
		return false
	}
	var out bytes.Buffer
	out.Write(b[:pos])
	fmt.Fprintf(&out, `<base href="%s">`, t.BaseURL())
	out.Write(b[pos:])
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(out.Bytes())
	}
	return true
}

// ServeStatic exposes only theme assets, never manifests, install staging or pointers.
func ServeStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, "/templates/")
	if rel == r.URL.Path || strings.Contains(rel, "\\") || strings.HasSuffix(rel, "/") {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(rel, "/")
	if len(parts) < 2 || !ValidKey(parts[0]) {
		http.NotFound(w, r)
		return
	}
	for _, p := range parts {
		if p == "" || strings.HasPrefix(p, ".") {
			http.NotFound(w, r)
			return
		}
	}
	key := parts[0]
	var dir, asset, rev string
	switch {
	case len(parts) >= 3 && revisionPattern.MatchString(parts[1]):
		rev = parts[1]
		dir = filepath.Join(Root, key, rev)
		asset = strings.Join(parts[2:], "/")
	case len(parts) >= 3 && parts[1] == "legacy":
		dir = filepath.Join(LegacyRoot, key)
		asset = strings.Join(parts[2:], "/")
	default:
		// Preserve old preview URLs; executable assets always need an explicit revision.
		asset = strings.Join(parts[1:], "/")
		if !imageFile(asset) {
			http.NotFound(w, r)
			return
		}
		t, err := Resolve(key)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		dir = t.Dir
	}
	if !allowedFile(asset) || path.Base(asset) == "theme.json" || path.Base(asset) == "meta.json" {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	f, err := root.Open(asset)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentTypes[strings.ToLower(path.Ext(asset))])
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if path.Ext(asset) == ".html" {
		w.Header().Set("Cache-Control", "no-store")
	} else if rev != "" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, st.Name(), time.Time{}, io.ReadSeeker(f))
}
