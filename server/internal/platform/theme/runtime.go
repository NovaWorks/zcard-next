package theme

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
)

type Runtime struct {
	Key            string          `json:"key"`
	ThemeRevision  string          `json:"theme_revision"`
	ConfigRevision string          `json:"config_revision"`
	Values         map[string]any  `json:"values"`
	Capabilities   map[string]bool `json:"capabilities"`
	Preview        bool            `json:"preview"`
	Theme          *Theme          `json:"-"`
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
	if r.Preview {
		out.WriteString(`<meta name="robots" content="noindex,nofollow"><script>`)
		out.WriteString(previewScript)
		out.WriteString(`</script>`)
	}
	out.Write(b[pos:])
	return out.Bytes()
}
