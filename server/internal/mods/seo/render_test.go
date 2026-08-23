package seo

// 动态渲染纯函数测试：商品/文章页 SEO head 与正文的关键字段。

import (
	"strings"
	"testing"
)

func testSite() siteInfo {
	return siteInfo{Name: "测试站", URL: "https://shop.example.com", VerifyGoogle: "g-token", VerifyBing: "b-token"}
}

func TestProductPageData(t *testing.T) {
	p := &ProductSEO{
		ID: 8, Name: "测试月卡", PriceCents: 1200,
		DescriptionHTML: "<p>自动发货</p><script>alert(1)</script>",
		Cover:           "https://cdn.example.com/cover.jpg",
	}
	d := productPageData(testSite(), "", p)
	if d.Title != "测试月卡 - 测试站" {
		t.Fatalf("title = %q", d.Title)
	}
	if d.Canonical != "https://shop.example.com/product/8" {
		t.Fatalf("canonical = %q", d.Canonical)
	}
	if !strings.Contains(string(d.JSONLD), `"@type":"Product"`) ||
		!strings.Contains(string(d.JSONLD), `"price":"12.00"`) ||
		!strings.Contains(string(d.JSONLD), `"@type":"BreadcrumbList"`) {
		t.Fatalf("jsonld 缺字段: %s", d.JSONLD)
	}
	html, err := renderPage(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<title>测试月卡 - 测试站</title>",
		`<link rel="canonical" href="https://shop.example.com/product/8">`,
		`<meta property="og:type" content="product">`,
		`<meta name="google-site-verification" content="g-token">`,
		"<h1>测试月卡</h1>",
		"¥12.00",
		"自动发货", // description 纯文本进 meta（script 已被 strip）
	} {
		if !strings.Contains(html, want) {
			t.Errorf("缺少 %q", want)
		}
	}
	if strings.Contains(d.Description, "alert") {
		t.Errorf("description 应剔除 script 内容: %q", d.Description)
	}
}

func TestPostPageData(t *testing.T) {
	p := &PostSEO{
		Slug: "notice-1", Title: "维护公告",
		Summary: "今晚维护", ContentHTML: "<p>23:00-24:00 维护</p>",
		PublishedAt: 1755900000,
	}
	d := postPageData(testSite(), "", p)
	if d.Title != "维护公告 - 测试站" || d.Canonical != "https://shop.example.com/posts/notice-1" {
		t.Fatalf("title/canonical = %q / %q", d.Title, d.Canonical)
	}
	if d.OGType != "article" || d.Description != "今晚维护" {
		t.Fatalf("ogType/desc = %q / %q", d.OGType, d.Description)
	}
	html, err := renderPage(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<meta property="og:type" content="article">`,
		"<h1>维护公告</h1>",
		"23:00-24:00 维护",
		`"@type":"Article"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("缺少 %q", want)
		}
	}
}

func TestStripTags(t *testing.T) {
	if got := stripTags("<p>a &amp; b</p><p>c&nbsp;d</p>"); got != "a & b c d" {
		t.Fatalf("stripTags = %q", got)
	}
	if got := truncateStr("一二三四五", 4); got != "一二三…" {
		t.Fatalf("truncate = %q", got)
	}
}
