package seo

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func TestSEOUsesConfiguredDefaultLanguage(t *testing.T) {
	handle, err := db.SQLite.Open(filepath.Join(t.TempDir(), "seo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	ctx := context.Background()
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	client.Setting.Create().SetGroup("i18n").SetKey("default_locale").SetValue([]byte(`"en"`)).SaveX(ctx)
	enabled := client.Setting.Create().SetGroup("i18n").SetKey("enabled_locales").SetValue([]byte(`["zh_CN","en"]`)).SaveX(ctx)
	client.Post.Create().SetSlug("guide").SetTitleJSON(map[string]string{"zh_CN": "使用说明", "en": "Guide"}).
		SetSummaryJSON(map[string]string{"zh_CN": "摘要", "en": "Summary"}).SetContentJSON(`{"zh_CN":"<p>正文</p>","en":"<p>English guide</p>"}`).SetIsPublished(true).SaveX(ctx)
	s := NewSeoService(NewSeoRepo(d), settings.NewRepoImpl(d), nil)
	shell := []byte(`<html lang="zh-CN"><head><title>Old title</title><script src="app.js"></script></head><body><div id="app"></div></body></html>`)
	req := httptest.NewRequest("GET", "/posts/guide", nil)
	// A browser's preferred language must not replace the configured first-visit default.
	req.Header.Set("Accept-Language", "zh-CN")
	output, status, err := s.RenderThemeHTML(req, shell)
	if err != nil || status != 200 {
		t.Fatalf("render failed: %d %v", status, err)
	}
	for _, want := range []string{`<html lang="en">`, "Guide - ZCard Store", "English guide", "News &amp; Guides", `src="app.js"`} {
		if !strings.Contains(string(output), want) {
			t.Errorf("English HTML missing %q: %s", want, output)
		}
	}
	private, _, err := s.RenderThemeHTML(httptest.NewRequest("GET", "/login", nil), shell)
	if err != nil || !strings.Contains(string(private), "Sign In - ZCard Store") {
		t.Fatalf("private page title not translated: %s %v", private, err)
	}
	client.Setting.UpdateOneID(enabled.ID).SetValue([]byte(`["zh_CN"]`)).SaveX(ctx)
	output, status, err = s.RenderThemeHTML(req, shell)
	if err != nil || status != 200 || !strings.Contains(string(output), `<html lang="zh-CN">`) || !strings.Contains(string(output), "使用说明") {
		t.Fatalf("disabled English did not fall back: %d %s %v", status, output, err)
	}
}
