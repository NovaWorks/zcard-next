package seo

// 爬虫动态渲染（Google 认可的 Dynamic Rendering 模式）：
// 爬虫请求商品/文章详情时实时从 DB 渲染完整 SEO HTML（title/canonical/og/
// JSON-LD + 正文），内容永远新鲜、删除即真 404；真人请求走静态页/SPA 链路不变。
// head 字段与前端 seo.ts 同口径（站点设置 + 实体数据自动生成）。

import (
	"context"
	"encoding/json"
	"html/template"
	"regexp"
	"strings"
)

// ── 站点设置装载 ────────────────────────────────────────────

// siteInfo 动态渲染所需站点配置（settings 公开键）。
type siteInfo struct {
	Name         string
	URL          string
	Logo         string
	SeoKeywords  string
	VerifyGoogle string
	VerifyBing   string
}

// base 站点 URL 基准（site.url 优先，空则 https://请求 Host）。
func (s siteInfo) base(host string) string {
	if s.URL != "" {
		return strings.TrimRight(s.URL, "/")
	}
	if host == "" {
		host = "localhost"
	}
	return "https://" + host
}

func (s *SeoService) loadSite(ctx context.Context) siteInfo {
	str := func(key string) string {
		raw, err := s.cfg.GetDefault(ctx, "site", key, nil)
		if err != nil || len(raw) == 0 {
			return ""
		}
		var v string
		if json.Unmarshal(raw, &v) == nil {
			return v
		}
		return ""
	}
	return siteInfo{
		Name:         str("name"),
		URL:          str("url"),
		Logo:         str("logo"),
		SeoKeywords:  str("seo_keywords"),
		VerifyGoogle: str("verification_google"),
		VerifyBing:   str("verification_bing"),
	}
}

// ── 纯文本工具（与前端 stripHtml/truncate 同口径）──────────

var (
	tagRe     = regexp.MustCompile(`<[^>]*>`)
	wsRe      = regexp.MustCompile(`\s+`)
	entityRe  = regexp.MustCompile(`&[a-zA-Z#0-9]+;`)
	scriptRe  = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
)

func stripTags(html string) string {
	out := scriptRe.ReplaceAllString(html, " ") // 脚本/样式内容不进 meta 描述
	out = tagRe.ReplaceAllString(out, " ")
	for k, v := range map[string]string{"&amp;": "&", "&lt;": "<", "&gt;": ">", "&quot;": `"`, "&#39;": "'"} {
		out = strings.ReplaceAll(out, k, v)
	}
	out = strings.ReplaceAll(out, "&nbsp;", " ")
	out = entityRe.ReplaceAllString(out, " ")
	return strings.TrimSpace(wsRe.ReplaceAllString(out, " "))
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
	ogImage := p.Cover
	if ogImage == "" && len(p.Images) > 0 {
		ogImage = p.Images[0]
	}
	offers := map[string]any{
		"@type":         "Offer",
		"price":         priceYuan(p.PriceCents),
		"priceCurrency": "CNY",
		"availability":  "https://schema.org/InStock",
	}
	body := `<h1>` + template.HTMLEscapeString(p.Name) + `</h1>`
	if p.PriceCents > 0 {
		body += `<div class="price">¥` + priceYuan(p.PriceCents) + `</div>`
	}
	body += `<div class="content">` + p.DescriptionHTML + `</div>`
	return seoPageData{
		Site: site, Base: base,
		Title:       p.Name + " - " + siteName,
		Description: desc,
		Keywords:    strings.Join(nonEmpty(p.Name, siteName), ","),
		Canonical:   canonical,
		OGType:      "product",
		OGImage:     ogImage,
		JSONLD: jsonldOf([]any{
			map[string]any{
				"@context": "https://schema.org", "@type": "Product",
				"name": p.Name, "image": ogImage, "description": desc,
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
	canonical := base + "/posts/" + p.Slug
	desc := p.Summary
	if desc == "" {
		desc = truncateStr(stripTags(p.ContentHTML), 150)
	} else {
		desc = truncateStr(stripTags(desc), 150)
	}
	date := ""
	if p.PublishedAt > 0 {
		date = timeFmt(p.PublishedAt)
	}
	article := map[string]any{
		"@context": "https://schema.org", "@type": "Article",
		"headline": p.Title,
		"author":   map[string]string{"@type": "Organization", "name": siteName},
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
		Keywords:    strings.Join(nonEmpty(p.Title, siteName), ","),
		Canonical:   canonical,
		OGType:      "article",
		JSONLD: jsonldOf([]any{
			article,
			map[string]any{
				"@context": "https://schema.org", "@type": "BreadcrumbList",
				"itemListElement": []any{
					map[string]any{"@type": "ListItem", "position": 1, "name": siteName, "item": base + "/"},
					map[string]any{"@type": "ListItem", "position": 2, "name": "文章", "item": base + "/posts"},
					map[string]any{"@type": "ListItem", "position": 3, "name": p.Title, "item": canonical},
				},
			},
		}),
		Crumbs: []crumb{{Name: siteName, URL: base + "/"}, {Name: "文章", URL: base + "/posts"}, {Name: p.Title}},
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
