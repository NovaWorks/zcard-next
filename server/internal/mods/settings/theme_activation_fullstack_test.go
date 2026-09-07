//go:build fullstack

package settings

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/theme"
	"github.com/NovaWorks/zcard-next/server/internal/web"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

type activationRepo struct {
	themeMemoryRepo
	failWrites bool
}

func (r *activationRepo) Put(ctx context.Context, g, k string, v json.RawMessage) error {
	if r.failWrites {
		return errors.New("database unavailable")
	}
	return r.themeMemoryRepo.Put(ctx, g, k, v)
}
func (r *activationRepo) PutMany(ctx context.Context, items []port.Item) error {
	if r.failWrites {
		return errors.New("database unavailable")
	}
	return r.themeMemoryRepo.PutMany(ctx, items)
}

// Exercises the actual upload, settings and storefront HTTP handlers together.
// In particular, installing v2 of an ACTIVE theme must continue serving v1.
func TestThemeInstallRequiresExplicitActivation(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	repo := &activationRepo{themeMemoryRepo: themeMemoryRepo{values: map[string]json.RawMessage{}}}
	svc := NewAdminSettingsService(NewSettingsUsecase(repo))
	s := khttp.NewServer()
	adminv1.RegisterAdminSettingsServiceHTTPServer(s, svc)
	RegisterTemplateStatic(s)
	s.HandlePrefix("/", web.NewStorefrontHandler(nil, svc.ActiveTheme))
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("User-Agent", "Mozilla/5.0 iPhone Mobile")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	upload := func(version string) {
		t.Helper()
		z := buildZip(t, map[string]string{
			"demo/theme.json": `{"name":"Demo","version":"` + version + `"}`,
			"demo/index.html": `<html><head><script src="./main.js"></script></head><body>DEMO_` + version + `</body></html>`,
			"demo/main.js":    "console.log('" + version + "')",
		})
		raw, _ := json.Marshal(map[string]string{"data_base64": z})
		if w := request("POST", "/api/v1/admin/settings/templates/install", string(raw)); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	activate := func(key string, wantCode int) {
		t.Helper()
		body := `{"items":[{"group":"template","key":"pc_template","value_json":"\"` + key + `\""}]}`
		if w := request("PUT", "/api/v1/admin/settings", body); w.Code != wantCode {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	assertPage := func(marker string) {
		t.Helper()
		for _, path := range []string{"/", "/product/123", "/login"} {
			w := request("GET", path, "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), marker) {
				t.Fatalf("%s did not serve %s: %d", path, marker, w.Code)
			}
		}
	}
	classic := request("GET", "/", "").Body.String()
	upload("1")
	if request("GET", "/", "").Body.String() != classic {
		t.Fatal("upload activated new theme")
	}
	if w := request("GET", "/api/v1/admin/settings/templates", ""); !strings.Contains(w.Body.String(), `"key":"demo"`) {
		t.Fatal("uploaded theme missing from list", w.Body.String())
	}
	activate("demo", 200)
	assertPage("DEMO_1")
	v1 := svc.ActiveTheme(ctx)
	upload("2")
	assertPage("DEMO_1")
	latest, err := theme.Resolve("demo")
	if err != nil || latest.Version != "2" || latest.Revision == v1.Revision {
		t.Fatal("new revision not installed", err)
	}
	// Selection is persisted; reconstructing services must not pick up installed v2.
	restarted := NewAdminSettingsService(NewSettingsUsecase(repo))
	if got := restarted.ActiveTheme(ctx); got == nil || got.Revision != v1.Revision {
		t.Fatal("restart activated pending upgrade")
	}
	// Neither unrelated saves nor a failed activation may replace the active revision.
	if w := request("PUT", "/api/v1/admin/settings", `{"items":[{"group":"template","key":"show_stock","value_json":"false"}]}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	assertPage("DEMO_1")
	repo.failWrites = true
	activate("demo", 400)
	assertPage("DEMO_1")
	repo.failWrites = false
	activate("demo", 200)
	assertPage("DEMO_2")
	if w := request("GET", v1.BaseURL()+"main.js", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "'1'") {
		t.Fatal("old page lost its original chunks")
	}
	// Internal snapshot is hidden and cannot be edited through public settings APIs.
	if w := request("GET", "/api/v1/admin/settings?group=template", ""); strings.Contains(w.Body.String(), activeThemeKey) {
		t.Fatal("internal setting visible")
	}
	if w := request("PUT", "/api/v1/admin/settings/template/active_theme", `{"value_json":"null"}`); w.Code != 400 {
		t.Fatal("snapshot writable", w.Code)
	}
	activate("classic", 200)
	if request("GET", "/", "").Body.String() != classic {
		t.Fatal("Classic did not return")
	}
}

func TestLegacyThemePinnedBeforeSameKeyUpload(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	pack := func(version string) string {
		return buildZip(t, map[string]string{
			"old/theme.json": `{"name":"Old","version":"` + version + `"}`,
			"old/index.html": `<html><head></head><body>` + version + `</body></html>`,
		})
	}
	raw, _ := base64.StdEncoding.DecodeString(pack("1"))
	old, err := theme.Install(raw)
	if err != nil {
		t.Fatal(err)
	}
	repo := &activationRepo{themeMemoryRepo: themeMemoryRepo{values: map[string]json.RawMessage{"template.pc_template": json.RawMessage(`"old"`)}}}
	svc := NewAdminSettingsService(NewSettingsUsecase(repo))
	// If the snapshot cannot be persisted, do not publish the new package.
	repo.failWrites = true
	if _, err := svc.InstallTemplate(ctx, &adminv1.InstallTemplateRequest{DataBase64: pack("2")}); err == nil {
		t.Fatal("installation proceeded without preserving legacy selection")
	}
	if latest, _ := theme.Resolve("old"); latest.Revision != old.Revision {
		t.Fatal("failed pin still published package")
	}
	repo.failWrites = false
	if _, err := svc.InstallTemplate(ctx, &adminv1.InstallTemplateRequest{DataBase64: pack("2")}); err != nil {
		t.Fatal(err)
	}
	if active := svc.ActiveTheme(ctx); active == nil || active.Revision != old.Revision {
		t.Fatal("legacy selection followed newly installed version")
	}
}
