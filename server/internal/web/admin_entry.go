package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/platform/adminpath"
)

// NewEntryHandler checks the current private entry before serving either SPA.
func NewEntryHandler(storefront http.Handler, admin func(http.ResponseWriter, *http.Request, string), entry func(context.Context) (string, error), legacyBase string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Public roots and reserved storefront prefixes cannot be private entries.
		// Keep their assets independent of settings reads on every request.
		first := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
		if _, err := adminpath.Normalize(first); first == "" || err != nil {
			storefront.ServeHTTP(w, r)
			return
		}
		base, err := entry(r.Context())
		if err == nil {
			base, err = adminpath.Normalize(base)
		}
		if err != nil || base == "" {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "入口配置暂不可用", http.StatusServiceUnavailable)
			return
		}
		matches := func(p string) bool { return r.URL.Path == p || strings.HasPrefix(r.URL.Path, p+"/") }
		if matches(base) {
			if r.URL.Path == base {
				http.Redirect(w, r, base+"/", http.StatusTemporaryRedirect)
				return
			}
			admin(w, r, base)
			return
		}
		for _, old := range []string{"/admin", legacyBase} {
			if old != "" && matches(old) {
				w.Header().Set("Cache-Control", "no-store")
				http.NotFound(w, r)
				return
			}
		}
		storefront.ServeHTTP(w, r)
	})
}
