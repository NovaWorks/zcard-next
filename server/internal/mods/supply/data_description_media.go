package supply

import (
	"context"
	"net/url"
	"path"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"golang.org/x/net/html"
)

const maxDescriptionImages = 20

// descriptionFor harvests upstream inline images before the product write. The
// catalog's existing RichHTML policy remains responsible for HTML sanitization.
func (s *SyncService) descriptionFor(ctx context.Context, mapping *ent.SupplyMapping, conn *ent.SupplyConnection, description string) string {
	if mapping != nil && mapping.LocalProductID > 0 {
		row, err := data.Client(ctx, s.repo.data).Product.Get(ctx, mapping.LocalProductID)
		if err == nil && (row.IsLocked || row.DescriptionProtected) {
			return row.Description
		}
	}
	if description == "" {
		return ""
	}

	resolved := map[string]string{}
	downloads := 0
	dir := ""
	z := html.NewTokenizer(strings.NewReader(description))
	var out strings.Builder
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			break
		}
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			out.Write(z.Raw())
			continue
		}
		token := z.Token()
		if token.Data != "img" {
			out.Write(z.Raw())
			continue
		}
		src := resolveUpstreamURL(conn.BaseURL, descriptionImageSource(token.Attr))
		local, exists := resolved[src]
		if !exists {
			local = src
			if src != "" && downloads < maxDescriptionImages && ctx.Err() == nil {
				if dir == "" {
					dir = s.ensureCoverDir(ctx, conn)
				}
				downloads++
				local = s.downloadCover(ctx, conn.BaseURL, src, dir)
			}
			resolved[src] = local
		}
		attrs := make([]html.Attribute, 0, len(token.Attr)+1)
		for _, attr := range token.Attr {
			if attr.Key != "src" {
				attrs = append(attrs, attr)
			}
		}
		if local != "" {
			attrs = append(attrs, html.Attribute{Key: "src", Val: local})
		}
		token.Attr = attrs
		out.WriteString(token.String())
	}
	return out.String()
}

func descriptionImageSource(attrs []html.Attribute) string {
	values := map[string]string{}
	for _, attr := range attrs {
		values[attr.Key] = strings.TrimSpace(attr.Val)
	}
	src := values["src"]
	if !placeholderImageSource(src) {
		return src
	}
	for _, key := range []string{"data-src", "data-original", "data-lazy-src"} {
		candidate := values[key]
		if candidate == "" || strings.HasPrefix(candidate, "#") || strings.Contains(candidate, "\\") {
			continue
		}
		ref, err := url.Parse(candidate)
		if err == nil && ref.User == nil && (ref.Scheme == "" || strings.EqualFold(ref.Scheme, "http") || strings.EqualFold(ref.Scheme, "https")) && (!ref.IsAbs() || ref.Hostname() != "") {
			return candidate
		}
	}
	return src
}

func placeholderImageSource(src string) bool {
	if src == "" || strings.HasPrefix(src, "#") {
		return true
	}
	u, err := url.Parse(src)
	if err != nil || (u.Scheme != "" && strings.ToLower(u.Scheme) != "http" && strings.ToLower(u.Scheme) != "https") {
		return true
	}
	name := strings.ToLower(path.Base(u.Path))
	stem := strings.TrimSuffix(name, path.Ext(name))
	switch stem {
	case "blank", "loading", "placeholder", "spacer", "transparent", "lazy":
		return true
	}
	return false
}
