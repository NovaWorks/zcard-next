package settings

import (
	"context"
	"encoding/json"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/NovaWorks/zcard-next/server/internal/platform/theme"
	"google.golang.org/protobuf/types/known/emptypb"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestThemeDraftPublishResetRollbackAndIsolation(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	repo := &themeMemoryRepo{values: map[string]json.RawMessage{}}
	svc := NewAdminSettingsService(NewSettingsUsecase(repo))
	values := theme.ClassicSettings().Defaults()
	values["theme.content_width"] = float64(1300)
	save := func(action, expected string) *themeOptionsState {
		t.Helper()
		raw, _ := json.Marshal(values)
		_, err := svc.SaveThemeSettings(ctx, &adminv1.SaveThemeSettingsRequest{Key: "classic", ThemeRevision: "builtin", ExpectedRevision: expected, Action: action, ValuesJson: string(raw)})
		if err != nil {
			t.Fatal(err)
		}
		st, err := readThemeState(ctx, repo, "classic")
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	st := save("draft", "")
	runtime, err := svc.runtime(ctx)
	if err != nil || len(runtime.Values) != 0 {
		t.Fatal("draft leaked", err)
	}
	raw, _ := json.Marshal(values)
	if _, e := svc.SaveThemeSettings(ctx, &adminv1.SaveThemeSettingsRequest{Key: "classic", Action: "publish", ValuesJson: string(raw)}); e == nil {
		t.Fatal("stale revision overwrote draft")
	}
	st = save("publish", st.Revision)
	runtime, _ = svc.runtime(ctx)
	if runtime.Values["theme.content_width"] != float64(1300) {
		t.Fatal("publish not applied")
	}
	public, err := NewStorefrontConfigService(repo).GetPublicConfig(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range public.Entries {
		if strings.Contains(e.Key, "_theme_settings") {
			t.Fatal("history exposed")
		}
		if e.Key == "theme.content_width" && e.ValueJson == "1300" {
			found = true
		}
	}
	if !found {
		t.Fatal("public config missing published value")
	}
	sub := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 7})
	other, _ := svc.runtime(sub)
	if len(other.Values) != 0 {
		t.Fatal("site settings leaked")
	}
	st = save("reset", st.Revision)
	runtime, _ = svc.runtime(ctx)
	if runtime.Values["theme.content_width"] != float64(1300) {
		t.Fatal("reset changed visitors before publish")
	}
	values = st.Draft.Values
	st = save("publish", st.Revision)
	st = save("rollback", st.Revision)
	runtime, _ = svc.runtime(ctx)
	if runtime.Values["theme.content_width"] != float64(1300) {
		t.Fatal("rollback failed")
	}
	got, e := svc.GetThemeSettings(ctx, &adminv1.ThemeSettingsRequest{Key: "classic"})
	if e != nil || !strings.Contains(got.StateJson, `"active":true`) {
		t.Fatal("Classic not active", e)
	}
}
func TestThemePreviewReadOnlyAndExpiry(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	repo := &themeMemoryRepo{values: map[string]json.RawMessage{"trade.cart_enabled": json.RawMessage("false")}}
	svc := NewAdminSettingsService(NewSettingsUsecase(repo))
	values := theme.ClassicSettings().Defaults()
	values["theme.content_width"] = float64(1400)
	raw, _ := json.Marshal(values)
	p, err := svc.PreviewThemeSettings(ctx, &adminv1.SaveThemeSettingsRequest{Key: "classic", ValuesJson: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(p.Url)
	token := u.Query().Get("theme_preview")
	h := PreviewWriteGuard(svc.ThemeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := theme.RuntimeFromContext(r.Context())
		if v != nil {
			json.NewEncoder(w).Encode(v)
		}
	})))
	request := func(method, path, header string, site uint64) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		if header != "" {
			r.Header.Set("X-Theme-Preview", header)
		}
		r = r.WithContext(tenancy.WithContext(r.Context(), tenancy.Context{SubsiteID: site}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", p.Url, "", 0); w.Code != 200 || !strings.Contains(w.Body.String(), "1400") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("preview not isolated", w)
	}
	if w := request("GET", "/api/v1/storefront/config", token, 0); w.Code != 200 || !strings.Contains(w.Body.String(), `"cart":false`) {
		t.Fatal("preview API capability mismatch", w)
	}
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		if w := request(method, "/api/v1/storefront/orders", token, 0); w.Code != 403 {
			t.Fatal("preview mutation allowed", method)
		}
	}
	if w := request("GET", p.Url, "", 9); w.Code != 410 {
		t.Fatal("cross-site token allowed")
	}
	if w := request("GET", "/", "", 0); strings.Contains(w.Body.String(), "1400") {
		t.Fatal("preview affected visitors")
	}
	themePreviews.Lock()
	old := themePreviews.items[token]
	old.Expires = time.Now().Add(-time.Second)
	themePreviews.items[token] = old
	themePreviews.Unlock()
	if w := request("GET", p.Url, "", 0); w.Code != 410 {
		t.Fatal("expired preview allowed")
	}
}
