package theme

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestThemeSettingsValidationAndMigration(t *testing.T) {
	s := ClassicSettings()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	values := s.Defaults()
	values["trade.cart_enabled"] = true
	if s.ValidateValues(values) == nil {
		t.Fatal("business override accepted")
	}
	delete(values, "trade.cart_enabled")
	values["theme.content_width"] = 1000.5
	if s.ValidateValues(values) == nil {
		t.Fatal("fractional dimension accepted")
	}
	values = s.Defaults()
	values["theme.primary_color"] = "url(javascript:alert(1))"
	if s.ValidateValues(values) == nil {
		t.Fatal("unsafe color accepted")
	}
	f := SettingsField{Key: "theme.new_name", Label: "New", Type: "text", Default: "default", RenamedFrom: "theme.old_name"}
	migrated := &SettingsSchema{Version: 1, Groups: []SettingsGroup{{ID: "a", Label: "A", Fields: []SettingsField{f}}}}
	if err := migrated.Validate(); err != nil {
		t.Fatal(err)
	}
	if migrated.Normalize(map[string]any{"theme.old_name": "saved"})[f.Key] != "saved" {
		t.Fatal("rename lost value")
	}
	if migrated.Normalize(map[string]any{f.Key: true})[f.Key] != "default" {
		t.Fatal("invalid old type not defaulted")
	}
	f.Key = "template.show_reviews"
	migrated.Groups[0].Fields[0] = f
	if migrated.Validate() == nil {
		t.Fatal("business capability override accepted")
	}
}
func TestRuntimeEscapesContentAndPrecedesApp(t *testing.T) {
	html := []byte(`<html><head><script src="app.js"></script></head><body></body></html>`)
	ctx := WithRuntime(context.Background(), &Runtime{Values: map[string]any{"theme.text": "</script><script>alert(1)</script>"}, Preview: true})
	out := string(InjectRuntime(html, ctx))
	if strings.Contains(out, "<script>alert(1)") {
		t.Fatal("HTML injection")
	}
	if strings.Index(out, "zcard-theme-runtime") > strings.Index(out, "app.js") {
		t.Fatal("runtime injected too late")
	}
	if !strings.Contains(out, "noindex,nofollow") {
		t.Fatal("preview indexed")
	}
	if string(InjectRuntime(html, context.Background())) != string(html) {
		t.Fatal("unconfigured HTML changed")
	}
}

func TestProvidedThemeSettingsPackage(t *testing.T) {
	path := os.Getenv("ZCARD_TEST_THEME_ZIP")
	if path == "" {
		t.Skip("optional compiled theme package")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	installed, err := Install(raw)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := LoadSettings(installed)
	if err != nil || schema == nil {
		t.Fatal("settings missing", err)
	}
	if err = schema.ValidateValues(schema.Defaults()); err != nil {
		t.Fatal(err)
	}
}
