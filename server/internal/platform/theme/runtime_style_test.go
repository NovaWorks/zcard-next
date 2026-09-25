package theme

import (
	"context"
	"strings"
	"testing"
)

func TestFirstPaintStyleUsesPublishedConfigAndRejectsCSSInjection(t *testing.T) {
	page := []byte(`<html><head><link rel="stylesheet" href="app.css"><script src="app.js"></script></head><body></body></html>`)
	runtime := &Runtime{PublicConfig: &PublicConfig{Entries: []ConfigEntry{
		{Key: "theme.primary_color", ValueJSON: `"#f8a245"`},
	}}, Values: map[string]any{"theme.content_width": float64(1280)}}
	html := string(InjectRuntime(page, WithRuntime(context.Background(), runtime)))
	for _, value := range []string{"--zc-primary:#f8a245;", "--zc-content-width:1280px;"} {
		if !strings.Contains(html, value) {
			t.Fatalf("first paint is missing %q", value)
		}
	}
	if strings.Index(html, "zcard-theme-style") > strings.Index(html, "app.css") {
		t.Fatal("style arrives after app stylesheet")
	}
	runtime.Values["theme.primary_color"] = "</style><script>alert(1)</script>"
	runtime.Values["theme.content_width"] = float64(100000)
	html = string(InjectRuntime(page, WithRuntime(context.Background(), runtime)))
	if strings.Contains(html, "<script>alert(1)") || strings.Contains(html, "--zc-content-width:") || strings.Contains(html, "--zc-primary:") {
		t.Fatal("unsafe CSS injected")
	}
	// Preview takes priority over saved configuration before JS as well.
	runtime.Values["theme.primary_color"] = "#112233"
	if !strings.Contains(string(InjectRuntime(page, WithRuntime(context.Background(), runtime))), "--zc-primary:#112233;") {
		t.Fatal("preview was overridden by saved settings")
	}
}
