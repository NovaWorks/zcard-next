package theme

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type Runtime struct {
	Key            string          `json:"key"`
	ThemeRevision  string          `json:"theme_revision"`
	ConfigRevision string          `json:"config_revision"`
	Values         map[string]any  `json:"values"`
	Capabilities   map[string]bool `json:"capabilities"`
	Preview        bool            `json:"preview"`
	Theme          *Theme          `json:"-"`
	PublicConfig   *PublicConfig   `json:"public_config,omitempty"`
	Branding       *Branding       `json:"branding,omitempty"`
}

// PublicConfig is populated only through the storefront public-key whitelist.
type PublicConfig struct {
	Entries []ConfigEntry `json:"entries"`
}
type ConfigEntry struct {
	Key       string `json:"key"`
	ValueJSON string `json:"value_json"`
}

// Branding contains only the public identity needed before the config API loads.
type Branding struct {
	Name string `json:"name"`
	Logo string `json:"logo"`
}
type runtimeKey struct{}

func WithRuntime(ctx context.Context, v *Runtime) context.Context {
	return context.WithValue(ctx, runtimeKey{}, v)
}
func RuntimeFromContext(ctx context.Context) *Runtime {
	v, _ := ctx.Value(runtimeKey{}).(*Runtime)
	return v
}

//go:embed preview.js
var previewScript string

func InjectRuntime(b []byte, ctx context.Context) []byte {
	r := RuntimeFromContext(ctx)
	if r == nil {
		return b
	}
	pos, err := indexHeadEnd(b)
	if err != nil {
		return b
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return b
	}
	var out bytes.Buffer
	out.Write(b[:pos])
	out.WriteString(`<script type="application/json" id="zcard-theme-runtime">`)
	out.Write(raw)
	out.WriteString(`</script>`)
	out.WriteString(runtimeStyle(r))
	if r.Preview {
		out.WriteString(`<meta name="robots" content="noindex,nofollow"><script>`)
		out.WriteString(previewScript)
		out.WriteString(`</script>`)
	}
	out.Write(b[pos:])
	return out.Bytes()
}

var runtimeColor = regexp.MustCompile(`^#[a-fA-F0-9]{6}$`)

// CSS is available before the application bundle, including on a cold/slow load.
// Only bounded numbers and hexadecimal colors enter a style element.
func runtimeStyle(r *Runtime) string {
	values := map[string]any{}
	if r.PublicConfig != nil {
		for _, e := range r.PublicConfig.Entries {
			var v any
			if json.Unmarshal([]byte(e.ValueJSON), &v) == nil {
				values[e.Key] = v
			}
		}
	}
	for k, v := range r.Values {
		values[k] = v
	}
	var css strings.Builder
	if color, ok := values["theme.primary_color"].(string); ok && runtimeColor.MatchString(color) {
		fmt.Fprintf(&css, "--zc-primary:%s;", color)
	}
	for _, field := range []struct {
		key, css string
		min, max float64
	}{
		{"theme.content_width", "--zc-content-width", 960, 1600},
		{"theme.font_size", "--zc-font-size", 12, 20},
		{"theme.card_radius", "--zc-card-radius", 0, 32},
		{"theme.notice_width", "--zc-notice-width", 480, 1280},
		{"theme.article_width", "--zc-article-width", 640, 1280},
	} {
		if n, ok := values[field.key].(float64); ok && n >= field.min && n <= field.max {
			fmt.Fprintf(&css, "%s:%gpx;", field.css, n)
		}
	}
	if css.Len() == 0 {
		return ""
	}
	return `<style id="zcard-theme-style">:root:root{` + css.String() + `}</style>`
}
