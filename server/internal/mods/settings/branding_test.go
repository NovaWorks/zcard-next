package settings

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/platform/theme"
)

func TestStorefrontBrandingBootstrap(t *testing.T) {
	t.Chdir(t.TempDir())
	repo := &themeMemoryRepo{values: map[string]json.RawMessage{
		"site.name":       json.RawMessage(`"客户店铺 </script><script>alert(1)</script>"`),
		"site.logo":       json.RawMessage(`"/uploads/customer.png"`),
		"site.admin_path": json.RawMessage(`"/private-entry"`),
	}}
	svc := NewAdminSettingsService(NewSettingsUsecase(repo))
	h := svc.ThemeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(theme.InjectRuntime([]byte(`<html><head><script src="app.js"></script></head><body></body></html>`), r.Context()))
	}))
	request := func() string {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	readBrand := func(html string) theme.Branding {
		t.Helper()
		const start = `<script type="application/json" id="zcard-theme-runtime">`
		_, raw, ok := strings.Cut(html, start)
		if !ok {
			t.Fatal("missing bootstrap")
		}
		raw, _, _ = strings.Cut(raw, "</script>")
		var runtime theme.Runtime
		if err := json.Unmarshal([]byte(raw), &runtime); err != nil || runtime.Branding == nil {
			t.Fatal("invalid branding bootstrap", err)
		}
		return *runtime.Branding
	}
	html := request()
	brand := readBrand(html)
	if brand.Name != "客户店铺 </script><script>alert(1)</script>" || brand.Logo != "/uploads/customer.png" {
		t.Fatal("bootstrap does not match settings", brand)
	}
	if strings.Contains(html, "<script>alert(1)") || strings.Contains(html, "/private-entry") {
		t.Fatal("unsafe or private setting exposed")
	}
	if strings.Index(html, "zcard-theme-runtime") > strings.Index(html, "app.js") {
		t.Fatal("identity arrives after app")
	}
	repo.values["site.name"] = json.RawMessage(`"更新后的店铺"`)
	repo.values["site.logo"] = json.RawMessage(`""`)
	if brand := readBrand(request()); brand.Name != "更新后的店铺" || brand.Logo != "" {
		t.Fatal("refresh retained stale branding", brand)
	}
	repo.values["site.name"] = json.RawMessage(`42`)
	if strings.Contains(request(), `"branding"`) {
		t.Fatal("invalid identity should fall back to the config API")
	}
}
