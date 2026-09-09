package settings

import (
	"bytes"
	"html"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/platform/sanitize"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	renderhtml "github.com/yuin/goldmark/renderer/html"
)

var announcementMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(renderhtml.WithHardWraps()),
)

// Keep the saved Markdown intact; expose only sanitized HTML and plain text to themes.
func renderAnnouncement(source string) (string, string) {
	var buf bytes.Buffer
	if err := announcementMarkdown.Convert([]byte(source), &buf); err != nil {
		return "", source
	}
	rendered := sanitize.HTML(buf.String())
	summary := strings.Join(strings.Fields(html.UnescapeString(sanitize.Text(rendered))), " ")
	return rendered, summary
}
