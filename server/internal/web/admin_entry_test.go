package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminEntryRuntimeChange(t *testing.T) {
	base := "/admin"
	var readErr error
	entry := NewEntryHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("storefront")) }), func(w http.ResponseWriter, r *http.Request, base string) { w.Write([]byte("private:" + base)) }, func(context.Context) (string, error) { return base, readErr }, "/legacy")
	check := func(path string, status int, body string) *httptest.ResponseRecorder {
		t.Helper()
		rr := httptest.NewRecorder()
		entry.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		if rr.Code != status || !strings.Contains(rr.Body.String(), body) {
			t.Fatalf("%s: %d %s", path, rr.Code, rr.Body.String())
		}
		return rr
	}
	check("/admin/", 200, "private:/admin")
	base = "/manage-72"
	if rr := check(base, 307, ""); rr.Header().Get("Location") != "/manage-72/" {
		t.Fatal(rr.Header())
	}
	for _, p := range []string{"/manage-72/", "/manage-72/settings", "/manage-72/assets/app.js"} {
		check(p, 200, "private:/manage-72")
	}
	for _, p := range []string{"/admin", "/admin/", "/admin/settings", "/admin/assets/app.js", "/legacy/"} {
		rr := check(p, 404, "")
		if strings.Contains(rr.Body.String(), base) || rr.Header().Get("Location") != "" {
			t.Fatal("private entry disclosed")
		}
	}
	check("/products", 200, "storefront")
	check("/manage-72-other", 200, "storefront")
	base = "/new-entry"
	check("/manage-72/", 200, "storefront")
	check("/new-entry/", 200, "private:/new-entry")
	readErr = errors.New("database offline")
	check("/products", 200, "storefront")
	check("/assets/app.js", 200, "storefront")
	check("/admin/", 503, "")
	check("/new-entry/", 503, "")
	readErr = nil
	base = "/api"
	check("/admin/", 503, "")
}
