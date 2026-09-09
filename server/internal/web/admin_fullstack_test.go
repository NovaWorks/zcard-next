//go:build fullstack

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPrivateAdminDocumentAndAssets(t *testing.T) {
	files := fstest.MapFS{"index.html": {Data: []byte(`<html><head><base href="/admin/"><meta name="zcard-admin-base" content="/admin/"><script src="./assets/app.js"></script></head><body>admin</body></html>`)}, "assets/app.js": {Data: []byte("app-code")}}
	admin := newHandler(files, "/admin", nil)
	handler := NewEntryHandler(http.NotFoundHandler(), admin.ServeAt, func(context.Context) (string, error) { return "/private/control", nil }, "/admin")
	for _, path := range []string{"/private/control/", "/private/control/settings", "/private/control/index.html?time=1"} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		if rr.Code != 200 || rr.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rr.Body.String(), `<base href="/private/control/">`) || !strings.Contains(rr.Body.String(), `name="zcard-admin-base" content="/private/control/"`) {
			t.Fatalf("%s %d %s %v", path, rr.Code, rr.Body.String(), rr.Header())
		}
		if strings.Contains(rr.Body.String(), "/admin/") {
			t.Fatal("old admin base retained")
		}
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/private/control/assets/app.js", nil))
	if rr.Code != 200 || rr.Body.String() != "app-code" || !strings.Contains(rr.Header().Get("Cache-Control"), "immutable") {
		t.Fatal(rr)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("HEAD", "/private/control/index.html", nil))
	if rr.Code != 200 || rr.Body.Len() != 0 || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(rr)
	}
	if !strings.Contains(string(admin.indexBytes), `href="/admin/"`) || admin.prefix != "/admin" {
		t.Fatal("shared handler mutated")
	}
}
