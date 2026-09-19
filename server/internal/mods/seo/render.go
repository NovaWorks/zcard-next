package seo

// Public metadata and fallback HTML share current site settings and catalog data.

import (
	"context"
	"encoding/json"
	htmlnode "golang.org/x/net/html"
	"html/template"
	"net/url"
	"strings"
)

// ── 站点设置装载 ────────────────────────────────────────────

// siteInfo 动态渲染所需站点配置（settings 公开键）。
type siteInfo struct {
	Name         string
	SeoTitle     string
	SeoDesc      string
	Currency     string
	URL          string
	Logo         string
	SeoKeywords  string
	VerifyGoogle string
	VerifyBing   string
}

// base 站点 URL 基准（site.url 优先，空则 https://请求 Host）。
func (s siteInfo) base(host string) string {
	if u, err := url.Parse(strings.TrimSpace(s.URL)); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil {
		u.RawQuery = ""
		u.Fragment = ""
		return strings.TrimRight(u.String(), "/")
	}
	if host == "" {
		host = "localhost"
	}
	return "https://" + host
}

func (s *SeoService) loadSite(ctx context.Context) siteInfo {
	str := func(key string) string {
		if s.cfg == nil {
			return ""
		}
		group := "site"
		if key == "base_currency" {
			group = "i18n"
		}
		raw, err := s.cfg.GetDefault(ctx, group, key, nil)
		if err != nil || len(raw) == 0 {
			return ""
		}
		var v string
		if json.Unmarshal(raw, &v) == nil {
			return strings.TrimSpace(v)
		}
		return ""
	}
	return siteInfo{
		Name:         orDefaultStr(str("name"), "ZCard 商店"),
		SeoTitle:     str("seo_title"),
		SeoDesc:      str("seo_desc"),
		Currency:     orDefaultStr(str("base_currency"), "CNY"),
		URL:          str("url"),
		Logo:         str("logo"),
		SeoKeywords:  str("seo_keywords"),
		VerifyGoogle: str("verification_google"),
		VerifyBing:   str("verification_bing"),
	}
}

// ── 纯文本工具（与前端 stripHtml/truncate 同口径）──────────

func stripTags(markup string) string {
	doc, err := htmlnode.Parse(strings.NewReader(markup))
	if err != nil {
		return ""
	}
	var out strings.Builder
	var walk func(*htmlnode.Node)
	walk = func(n *htmlnode.Node) {
		if n.Type == htmlnode.ElementNode {
			switch n.Data {
			case "head", "script", "style", "template", "noscript":
				return
			}
		}
		if n.Type == htmlnode.TextNode {
			out.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == htmlnode.ElementNode {
			switch n.Data {
			case "p", "div", "h1", "h2", "h3", "h4", "h5", "h6", "li", "tr", "br":
				out.WriteByte(' ')
			}
		}
	}
	walk(doc)
	return strings.Join(strings.Fields(out.String()), " ")
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// ── 页面模板 ────────────────────────────────────────────────

type crumb struct {
	Name string
	URL  string
}

type seoPageData struct {
	Site        siteInfo
	Base        string
	Title       string
	Description string
	Keywords    string
	Canonical   string
	Robots      string
	OGType      string
	OGImage     string
	JSONLD      template.HTML // json.Marshal 默认转义 <>& → script 上下文安全
	Crumbs      []crumb
	Body        template.HTML // 商品/文章正文（入库前已 sanitize）
}

var pageTmpl = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<meta name="description" content="{{.Description}}">
<meta name="keywords" content="{{.Keywords}}">
<meta name="robots" content="{{if .Robots}}{{.Robots}}{{else}}index,follow{{end}}">
<meta name="twitter:card" content="{{if .OGImage}}summary_large_image{{else}}summary{{end}}">
<meta name="twitter:title" content="{{.Title}}">
<meta name="twitter:description" content="{{.Description}}">
{{if .OGImage}}<meta name="twitter:image" content="{{.OGImage}}">{{end}}
<meta property="og:site_name" content="{{.Site.Name}}">
<meta property="og:title" content="{{.Title}}">
<meta property="og:description" content="{{.Description}}">
<meta property="og:type" content="{{.OGType}}">
<meta property="og:url" content="{{.Canonical}}">
{{if .OGImage}}<meta property="og:image" content="{{.OGImage}}">{{end}}
<link rel="canonical" href="{{.Canonical}}">
{{if .Site.VerifyGoogle}}<meta name="google-site-verification" content="{{.Site.VerifyGoogle}}">{{end}}
{{if .Site.VerifyBing}}<meta name="msvalidate.01" content="{{.Site.VerifyBing}}">{{end}}
<meta name="zcard-jsonld" content="1">
<style>body{font-family:system-ui,-apple-system,"PingFang SC",sans-serif;margin:0;color:#1f2329;line-height:1.75}
main{max-width:820px;margin:0 auto;padding:24px 16px}
.brand{padding:14px 16px;border-bottom:1px solid #e5e7eb;font-weight:700}
.brand a{color:inherit;text-decoration:none}
.crumbs{font-size:13px;color:#6b7280;margin-bottom:12px}.crumbs a{color:#2563eb;text-decoration:none}
h1{font-size:24px;margin:8px 0 12px}
.price{font-size:20px;color:#ff5722;font-weight:700;margin:8px 0}
.date{font-size:13px;color:#6b7280}
.content img{max-width:100%;border-radius:8px}
.content table{border-collapse:collapse;width:100%}.content td,.content th{border:1px solid #e5e7eb;padding:6px 10px}
.content pre{background:#0f172a;color:#e2e8f0;padding:12px;border-radius:8px;overflow-x:auto}
footer{border-top:1px solid #e5e7eb;margin-top:32px;padding:16px;color:#9ca3af;font-size:13px;text-align:center}</style>
</head>
<body>
<div class="brand"><a href="{{.Base}}/">{{.Site.Name}}</a></div>
<main>
<nav class="crumbs">{{range .Crumbs}}{{if .URL}}<a href="{{.URL}}">{{.Name}}</a>{{else}}{{.Name}}{{end}} › {{end}}</nav>
{{.Body}}
</main>
<footer>© {{.Site.Name}}</footer>
</body>
</html>
`))

// jsonldPlaceholder 模板占位（html/template 的 script 上下文会对值二次转义、
// HTML 注释会被剥离——占位用空 meta 标签，渲染后字符串替换注入 JSON-LD）。
const jsonldPlaceholder = `<meta name="zcard-jsonld" content="1">`

// renderPage 渲染完整爬虫页 HTML。
func renderPage(d seoPageData) (string, error) {
	var b strings.Builder
	if err := pageTmpl.Execute(&b, d); err != nil {
		return "", err
	}
	ld := ""
	if d.JSONLD != "" {
		ld = `<script type="application/ld+json">` + string(d.JSONLD) + `</script>`
	}
	return strings.Replace(b.String(), jsonldPlaceholder, ld, 1), nil
}

func jsonldOf(v any) template.HTML {
	b, _ := json.Marshal(v)
	return template.HTML(b)
}

// ── 商品/文章页数据组装（纯函数，可单测）────────────────────

func productPageData(site siteInfo, host string, p *ProductSEO) seoPageData {
	base := site.base(host)
	siteName := orDefaultStr(site.Name, "ZCard 商店")
	canonical := base + "/product/" + u64str(p.ID)
	desc := truncateStr(stripTags(p.DescriptionHTML), 150)
	if desc == "" {
		desc = truncateStr(stripTags(p.Name), 150)
	}
	ogImage := p.Cover
	ogImage = absoluteURL(orDefaultStr(ogImage, site.Logo), base)
	variants := p.Offers
	if len(variants) == 0 {
		variants = []ProductOfferSEO{{PriceCents: p.PriceCents, Stock: p.Stock}}
	}
	offerList := make([]any, 0, len(variants))
	for _, variant := range variants {
		offer := map[string]any{"@type": "Offer", "price": priceYuan(variant.PriceCents), "priceCurrency": orDefaultStr(site.Currency, "CNY"), "url": canonical, "seller": organization(site, base)}
		if variant.SKU != 0 {
			offer["sku"] = u64str(variant.SKU)
		}
		if variant.Stock >= -1 {
			offer["availability"] = "https://schema.org/InStock"
			if variant.Stock == 0 {
				offer["availability"] = "https://schema.org/OutOfStock"
			}
		}
		offerList = append(offerList, offer)
	}
	var offers any = offerList
	if len(offerList) == 1 {
		offers = offerList[0]
	}
	body := `<h1>` + template.HTMLEscapeString(p.Name) + `</h1>`
	if p.PriceCents > 0 {
		body += `<div class="price">` + template.HTMLEscapeString(orDefaultStr(site.Currency, "CNY")) + " " + priceYuan(p.PriceCents) + `</div>`
	}
	body += `<div class="content">` + p.DescriptionHTML + `</div>`
	return seoPageData{
		Site: site, Base: base,
		Title:       p.Name + " - " + siteName,
		Description: desc,
		Keywords:    strings.Join(nonEmpty(p.Name, site.SeoKeywords, siteName), ","),
		Canonical:   canonical,
		OGType:      "product",
		OGImage:     ogImage,
		JSONLD: jsonldOf([]any{
			map[string]any{
				"@context": "https://schema.org", "@type": "Product",
				"name": p.Name, "image": ogImage, "description": desc, "url": canonical,
				"offers": offers,
			},
			map[string]any{
				"@context": "https://schema.org", "@type": "BreadcrumbList",
				"itemListElement": []any{
					map[string]any{"@type": "ListItem", "position": 1, "name": siteName, "item": base + "/"},
					map[string]any{"@type": "ListItem", "position": 2, "name": "全部商品", "item": base + "/products"},
					map[string]any{"@type": "ListItem", "position": 3, "name": p.Name, "item": canonical},
				},
			},
		}),
		Crumbs: []crumb{{Name: siteName, URL: base + "/"}, {Name: "全部商品", URL: base + "/products"}, {Name: p.Name}},
		Body:   template.HTML(body),
	}
}

func postPageData(site siteInfo, host string, p *PostSEO) seoPageData {
	base := site.base(host)
	siteName := orDefaultStr(site.Name, "ZCard 商店")
	canonical := base + "/posts/" + url.PathEscape(p.Slug)
	desc := p.Summary
	if desc == "" {
		desc = truncateStr(stripTags(p.ContentHTML), 150)
	} else {
		desc = truncateStr(stripTags(desc), 150)
	}
	if desc == "" {
		desc = truncateStr(stripTags(orDefaultStr(p.ContentHTML, p.Title)), 150)
	}
	date := ""
	if p.PublishedAt > 0 {
		date = timeFmt(p.PublishedAt)
	}
	article := map[string]any{
		"@context": "https://schema.org", "@type": "Article",
		"headline":    p.Title,
		"author":      organization(site, base),
		"publisher":   organization(site, base),
		"description": desc, "url": canonical, "mainEntityOfPage": canonical,
	}
	if image := absoluteURL(orDefaultStr(p.Thumbnail, site.Logo), base); image != "" {
		article["image"] = image
	}
	if date != "" {
		article["datePublished"] = date
	}
	body := `<h1>` + template.HTMLEscapeString(p.Title) + `</h1>`
	if date != "" {
		body += `<div class="date">` + date + `</div>`
	}
	body += `<div class="content">` + p.ContentHTML + `</div>`
	return seoPageData{
		Site: site, Base: base,
		Title:       p.Title + " - " + siteName,
		Description: desc,
		Keywords:    strings.Join(nonEmpty(p.Title, site.SeoKeywords, siteName), ","),
		Canonical:   canonical,
		OGType:      "article",
		OGImage:     absoluteURL(orDefaultStr(p.Thumbnail, site.Logo), base),
		JSONLD: jsonldOf([]any{
			article,
			map[string]any{
				"@context": "https://schema.org", "@type": "BreadcrumbList",
				"itemListElement": []any{
					map[string]any{"@type": "ListItem", "position": 1, "name": siteName, "item": base + "/"},
					map[string]any{"@type": "ListItem", "position": 2, "name": "文章公告", "item": base + "/posts"},
					map[string]any{"@type": "ListItem", "position": 3, "name": p.Title, "item": canonical},
				},
			},
		}),
		Crumbs: []crumb{{Name: siteName, URL: base + "/"}, {Name: "文章公告", URL: base + "/posts"}, {Name: p.Title}},
		Body:   template.HTML(body),
	}
}

func nonEmpty(vs ...string) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func orDefaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func absoluteURL(value, base string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	b, err := url.Parse(base + "/")
	if err != nil {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil {
		return ""
	}
	u = b.ResolveReference(u)
	if (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return ""
	}
	return u.String()
}
func organization(site siteInfo, base string) map[string]any {
	out := map[string]any{"@type": "Organization", "name": site.Name, "url": base + "/"}
	if logo := absoluteURL(site.Logo, base); logo != "" {
		out["logo"] = logo
	}
	return out
}
