package settings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/theme"
)

type unavailablePublicRepo struct{ *themeMemoryRepo }

func (r unavailablePublicRepo) List(context.Context, string) ([]port.Item, error) {
	return nil, errors.New("database unavailable")
}

func TestStorefrontBootstrapPublicSnapshotAndClearing(t *testing.T) {
	t.Chdir(t.TempDir())
	repo := &themeMemoryRepo{values: map[string]json.RawMessage{
		"site.name": json.RawMessage(`"测试商店"`), "site.logo": json.RawMessage(`""`),
		"footer.about":              json.RawMessage(`"自定义简介 </script><script>bad()</script>"`),
		"theme.primary_color":       json.RawMessage(`"#f8a245"`),
		"notify.telegram_bot_token": json.RawMessage(`"private-token"`),
		"site.admin_path":           json.RawMessage(`"/private-admin"`),
	}}
	svc := NewAdminSettingsService(NewSettingsUsecase(repo))
	publish := func(color string) {
		values := theme.ClassicSettings().Defaults()
		values["theme.primary_color"] = color
		raw, _ := json.Marshal(values)
		state, err := readThemeState(context.Background(), repo, "classic")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SaveThemeSettings(context.Background(), &adminv1.SaveThemeSettingsRequest{Key: "classic", Action: "publish", ExpectedRevision: state.Revision, ValuesJson: string(raw)}); err != nil {
			t.Fatal(err)
		}
	}
	publish("#f8a245")
	handler := svc.ThemeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(theme.InjectRuntime([]byte(`<html><head></head><body></body></html>`), r.Context()))
	}))
	request := func() string {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		return w.Body.String()
	}
	html := request()
	if !strings.Contains(html, `"public_config"`) || !strings.Contains(html, "自定义简介") || !strings.Contains(html, "--zc-primary:#f8a245;") {
		t.Fatal("missing request-time appearance")
	}
	if strings.Contains(html, "private-token") || strings.Contains(html, "private-admin") || strings.Contains(html, "<script>bad()") {
		t.Fatal("unsafe public bootstrap")
	}
	repo.values["footer.about"] = json.RawMessage(`""`)
	publish("#112233")
	html = request()
	if strings.Contains(html, "自定义简介") || !strings.Contains(html, "--zc-primary:#112233;") {
		t.Fatal("reload retained old appearance")
	}
}

func TestPublicConfigFailureNeverReturnsDefaultsAsSuccess(t *testing.T) {
	repo := unavailablePublicRepo{&themeMemoryRepo{values: map[string]json.RawMessage{}}}
	if config, err := NewStorefrontConfigService(repo).GetPublicConfig(context.Background(), nil); err == nil || config != nil {
		t.Fatal("failed snapshot treated as valid defaults")
	}
	t.Chdir(t.TempDir())
	handler := NewAdminSettingsService(NewSettingsUsecase(repo)).ThemeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("served a false default storefront") }))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 {
		t.Fatalf("wanted recoverable failure, got %d", w.Code)
	}
}
