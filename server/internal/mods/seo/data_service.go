package seo

// robots.txt + sitemap.xml 生成（设置 site.url 优先作 URL 基准，未配置回落请求 Host）。

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
)

// SeoService SEO 基础服务。
type SeoService struct {
	repo *SeoRepo
	cfg  *settings.RepoImpl
}

// NewSeoService 构造。
func NewSeoService(repo *SeoRepo, cfg *settings.RepoImpl) *SeoService {
	return &SeoService{repo: repo, cfg: cfg}
}

// siteURL 站点 URL 基准：site.url 优先（去尾斜杠），空则 https://host 兜底。
func (s *SeoService) siteURL(ctx context.Context, host string) string {
	if raw, err := s.cfg.GetDefault(ctx, "site", "url", nil); err == nil && len(raw) > 0 {
		var v string
		if json.Unmarshal(raw, &v) == nil && v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	if host == "" {
		host = "localhost"
	}
	return "https://" + host
}

// RobotsTXT robots.txt：全站放行 + 自定义规则追加 + Sitemap 指向。
func (s *SeoService) RobotsTXT(ctx context.Context, host string) string {
	var b strings.Builder
	b.WriteString("User-agent: *\nAllow: /\n")
	if raw, err := s.cfg.GetDefault(ctx, "site", "robots_custom", nil); err == nil && len(raw) > 0 {
		var v string
		if json.Unmarshal(raw, &v) == nil && v != "" {
			b.WriteString("\n")
			b.WriteString(strings.TrimSpace(v))
			b.WriteString("\n")
		}
	}
	b.WriteString(fmt.Sprintf("\nSitemap: %s/sitemap.xml\n", s.siteURL(ctx, host)))
	return b.String()
}

// sitemapEntry sitemap URL 条目。
type sitemapEntry struct {
	Loc     string `xml:"loc"`
	Lastmod string `xml:"lastmod,omitempty"`
}

type sitemapURLSet struct {
	XMLName xml.Name      `xml:"urlset"`
	Xmlns   string        `xml:"xmlns,attr"`
	URLs    []sitemapEntry `xml:"url"`
}

const sitemapNS = "http://www.sitemaps.org/schemas/sitemap/0.9"

func lastmodOf(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format("2006-01-02")
}

// SitemapXML sitemap.xml：静态页 + 商品/文章/分类动态条目。
func (s *SeoService) SitemapXML(ctx context.Context, host string) (string, error) {
	base := s.siteURL(ctx, host)
	entries := []sitemapEntry{
		{Loc: base + "/"},
		{Loc: base + "/products"},
		{Loc: base + "/posts"},
	}

	products, err := s.repo.ListSitemapProducts(ctx)
	if err != nil {
		return "", err
	}
	for _, p := range products {
		entries = append(entries, sitemapEntry{
			Loc:     fmt.Sprintf("%s/product/%d", base, p.ID),
			Lastmod: lastmodOf(p.UpdatedAt),
		})
	}

	posts, err := s.repo.ListSitemapPosts(ctx)
	if err != nil {
		return "", err
	}
	for _, p := range posts {
		entries = append(entries, sitemapEntry{
			Loc:     fmt.Sprintf("%s/posts/%s", base, p.Slug),
			Lastmod: lastmodOf(p.PublishedAt),
		})
	}

	cats, err := s.repo.ListSitemapCategories(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range cats {
		entries = append(entries, sitemapEntry{Loc: fmt.Sprintf("%s/products?category_id=%d", base, c.ID)})
	}

	out, err := xml.MarshalIndent(sitemapURLSet{Xmlns: sitemapNS, URLs: entries}, "", "  ")
	if err != nil {
		return "", err
	}
	return xml.Header + string(out) + "\n", nil
}
