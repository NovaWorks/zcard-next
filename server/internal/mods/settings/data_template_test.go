package settings

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/theme"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildZip(t *testing.T, files map[string]string) string {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for n, c := range files {
		f, e := z.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		f.Write([]byte(c))
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
func TestInstallTemplateHTTPAndClassic(t *testing.T) {
	t.Chdir(t.TempDir())
	svc := &AdminSettingsService{}
	s := khttp.NewServer()
	adminv1.RegisterAdminSettingsServiceHTTPServer(s, svc)
	RegisterTemplateStatic(s)
	z := buildZip(t, map[string]string{"dark/theme.json": `{"name":"Dark","version":"1.0.0","preview":"preview.svg"}`, "dark/index.html": "<html><head></head><body>Dark</body></html>", "dark/preview.svg": "<svg/>"})
	r := httptest.NewRequest("POST", "/api/v1/admin/settings/templates/install", strings.NewReader(`{"data_base64":"`+z+`"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	items, e := scanTemplates()
	if e != nil || len(items) != 2 || items[0].Key != "classic" || items[1].Key != "dark" {
		t.Fatalf("%+v %v", items, e)
	}
	for _, key := range []string{"classic", "dark"} {
		if !templateKeyExists(key) {
			t.Fatal(key)
		}
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", items[1].Preview, nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	bad := buildZip(t, map[string]string{"dark/sub/theme.json": "{}"})
	if _, e = svc.InstallTemplate(context.Background(), &adminv1.InstallTemplateRequest{DataBase64: bad}); e == nil {
		t.Fatal("bad accepted")
	}
	if !templateKeyExists("dark") {
		t.Fatal("old theme lost")
	}
}
func TestLegacyRunnableTheme(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := filepath.Join(theme.LegacyRoot, "legacy")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "theme.json"), []byte(`{"name":"Legacy","version":"1"}`), 0644)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><head></head><body>legacy</body></html>"), 0644)
	items, e := scanTemplates()
	if e != nil || len(items) != 2 || items[1].Key != "legacy" {
		t.Fatalf("%+v %v", items, e)
	}
	os.Remove(filepath.Join(dir, "index.html"))
	if templateKeyExists("legacy") {
		t.Fatal("unrunnable legacy theme selectable")
	}
	if !templateKeyExists("classic") {
		t.Fatal("classic missing")
	}
}
