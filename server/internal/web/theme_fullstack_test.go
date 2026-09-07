//go:build fullstack

package web

import (
	"archive/zip"
	"bytes"
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/platform/theme"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResponsiveThemeRouting(t *testing.T) {
	t.Chdir(t.TempDir())
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for n, c := range map[string]string{"test/theme.json": `{"name":"Responsive","version":"1"}`, "test/index.html": `<html><head><script src="./main.js"></script></head><body>EXTERNAL_THEME_MARKER</body></html>`, "test/main.js": "console.log(1)"} {
		f, _ := z.Create(n)
		f.Write([]byte(c))
	}
	z.Close()
	installed, err := theme.Install(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	selected := "classic"
	h := NewStorefrontHandler(nil, func(context.Context) *theme.Theme { t, _ := theme.Resolve(selected); return t })
	get := func(p, ua string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", p, nil)
		r.Header.Set("User-Agent", ua)
		h.ServeHTTP(w, r)
		return w
	}
	classic := get("/", "Mozilla desktop").Body.String()
	selected = "test"
	for _, ua := range []string{"Mozilla desktop", "Mozilla iPhone Mobile"} {
		for _, p := range []string{"/", "/product/123", "/posts/intro.v2", "/login", "/user/orders", "/index.html"} {
			w := get(p, ua)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "EXTERNAL_THEME_MARKER") || !strings.Contains(w.Body.String(), installed.BaseURL()) {
				t.Fatalf("%s %s: %d %s", ua, p, w.Code, w.Body.String())
			}
		}
	}
	if w := get("/unknown.js", ""); w.Code != 404 {
		t.Fatal("missing JS got HTML")
	}
	for _, p := range []string{"/api/v1/storefront/config", "/payments/callback", "/uploads/a.png", "/install", "/health/ping", "/assets/old.js"} {
		if strings.Contains(get(p, "").Body.String(), "EXTERNAL_THEME_MARKER") {
			t.Fatalf("theme intercepted reserved route %s", p)
		}
	}
	// Admin remains embedded even when the storefront changes.
	w := httptest.NewRecorder()
	NewAdminHandler().ServeHTTP(w, httptest.NewRequest("GET", "/admin/home", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "EXTERNAL_THEME_MARKER") {
		t.Fatal("admin affected")
	}
	selected = "classic"
	if get("/", "").Body.String() != classic {
		t.Fatal("Classic did not return immediately")
	}
	selected = "missing"
	if get("/", "").Body.String() != classic {
		t.Fatal("missing theme did not fall back")
	}
	selected = "test"
	os.Remove(filepath.Join(installed.Dir, "index.html"))
	if get("/", "").Body.String() != classic {
		t.Fatal("broken active theme did not fall back")
	}
}
