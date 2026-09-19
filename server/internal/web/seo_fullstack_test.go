//go:build fullstack

package web_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/seo"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/NovaWorks/zcard-next/server/internal/platform/theme"
	"github.com/NovaWorks/zcard-next/server/internal/web"
)

func TestThemeSEORealCatalog(t *testing.T) {
	ctx := context.Background()
	handle, err := db.SQLite.Open("file:" + t.TempDir() + "/seo.db?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	for key, value := range map[string]string{"name": "SEO 商店", "url": "https://shop.example.com", "logo": "/uploads/logo.png", "seo_desc": "数字卡密与授权", "verification_google": "google-test"} {
		raw, _ := json.Marshal(value)
		client.Setting.Create().SetGroup("site").SetKey(key).SetValue(raw).SaveX(ctx)
	}
	client.Setting.Create().SetGroup("i18n").SetKey("base_currency").SetValue(json.RawMessage(`"USD"`)).SaveX(ctx)
	cat := client.Category.Create().SetName("社交账号").SaveX(ctx)
	client.Product.Create().SetID(688).SetCategoryID(cat.ID).SetName("测试账号").SetSlug("account").SetDescription("<p>真实商品说明</p>").SetPrice(253).SetCover("/uploads/account.png").SetStatus(1).SaveX(ctx)
	client.Product.Create().SetID(689).SetName("上游未知库存").SetSlug("unknown-stock").SetUpstreamSourceID(9).SetUpstreamProductCode("remote").SetPrice(399).SetStatus(1).SaveX(ctx)
	client.Product.Create().SetID(690).SetName("下架商品").SetSlug("unlisted").SetStatus(0).SaveX(ctx)
	client.Post.Create().SetSlug("使用说明").SetTitleJSON(map[string]string{"zh_CN": "账号使用说明"}).SetSummaryJSON(map[string]string{"zh_CN": "<p>文章摘要</p>"}).SetContentJSON(`{"zh_CN":"<p>文章正文</p>"}`).SetIsPublished(true).SetPublishedAt(time.Unix(1755900000, 0)).SaveX(ctx)
	store := catalog.NewStoreCatalogService(catalog.NewCatalogUsecase(catalog.NewProductRepoImpl(d, nil)), nil, nil)
	svc := seo.NewSeoService(seo.NewSeoRepo(d), settings.NewRepoImpl(d), store)
	t.Chdir(t.TempDir())
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	for path, content := range map[string]string{"seo/theme.json": `{"name":"SEO theme","version":"1"}`, "seo/index.html": `<html><head><meta name="zcard-seo" content="server-v1"><title>old</title><script src="./app.js"></script></head><body><div id="root"></div></body></html>`, "seo/app.js": "console.log('theme')"} {
		f, _ := archive.Create(path)
		f.Write([]byte(content))
	}
	archive.Close()
	installed, err := theme.Install(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	h := web.NewStorefrontHandler(svc, func(context.Context) *theme.Theme { return installed })
	get := func(method, path, ua string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("User-Agent", ua)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		path, title string
		status      int
	}{{"/", "SEO 商店", 200}, {"/products", "全部商品 - SEO 商店", 200}, {"/products?category_id=1", "社交账号 - SEO 商店", 200}, {"/points", "积分商城 - SEO 商店", 200}, {"/posts", "文章公告 - SEO 商店", 200}, {"/posts/使用说明", "账号使用说明 - SEO 商店", 200}, {"/product/688", "测试账号 - SEO 商店", 200}, {"/member", "个人中心 - SEO 商店", 200}, {"/lottery", "幸运抽奖 - SEO 商店", 200}, {"/lottery/1", "幸运抽奖 - SEO 商店", 200}, {"/lottery?tab=prizes", "我的奖品 - SEO 商店", 200}, {"/product/690", "页面不存在 - SEO 商店", 404}, {"/product/no", "页面不存在 - SEO 商店", 404}, {"/missing", "页面不存在 - SEO 商店", 404}} {
		normal := get("GET", tc.path, "Mozilla/5.0")
		bot := get("GET", tc.path, "Googlebot")
		if normal.Code != tc.status || bot.Code != tc.status || normal.Body.String() != bot.Body.String() {
			t.Fatalf("%s: browser=%d bot=%d", tc.path, normal.Code, bot.Code)
		}
		if !strings.Contains(normal.Body.String(), ">"+tc.title+"</title>") || !strings.Contains(normal.Body.String(), installed.BaseURL()) {
			t.Fatalf("%s: %s", tc.path, normal.Body.String())
		}
		head := get("HEAD", tc.path, "Googlebot")
		if head.Code != tc.status || head.Body.Len() != 0 {
			t.Fatal("HEAD semantics")
		}
		if tc.status == 404 || tc.path == "/member" || strings.HasPrefix(tc.path, "/lottery") {
			if !strings.Contains(normal.Body.String(), "noindex,nofollow") || strings.Contains(normal.Body.String(), `type="application/ld+json"`) {
				t.Fatal("private/missing page indexed")
			}
		}
	}
	out := get("GET", "/product/688", "Googlebot").Body.String()
	for _, want := range []string{"OutOfStock", `"priceCurrency":"USD"`, `"price":"2.53"`, "https://shop.example.com/uploads/account.png", "真实商品说明", "<noscript"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
	if out := get("GET", "/product/689", "").Body.String(); strings.Contains(out, "availability") {
		t.Fatal("unknown stock advertised")
	}
	client.Card.Create().SetProductID(688).SetContent([]byte("private-secret-never-render")).SetContentHash("card").SaveX(ctx)
	out = get("GET", "/product/688", "").Body.String()
	if !strings.Contains(out, "InStock") || strings.Contains(out, "private-secret-never-render") {
		t.Fatal("stock not refreshed or delivery secret exposed")
	}
	if !strings.Contains(get("GET", "/products", "").Body.String(), `href="https://shop.example.com/product/688"`) {
		t.Fatal("catalog has no crawlable links")
	}
	if !strings.Contains(svc.RobotsTXT(ctx, "localhost"), "/coupons") {
		t.Fatal("coupons missing from robots")
	}
	sitemap, err := svc.SitemapXML(ctx, "localhost")
	if err != nil || !strings.Contains(sitemap, "/product/688") || strings.Contains(sitemap, "/product/690") {
		t.Fatal("sitemap visibility", err)
	}
	client.Product.Create().SetID(691).SetSubsiteID(9).SetName("其他分站商品").SetSlug("other-site").SetStatus(1).SaveX(ctx)
	client.Post.Create().SetSubsiteID(9).SetSlug("other-post").SetTitleJSON(map[string]string{"zh_CN": "其他分站文章"}).SetContentJSON(`{"zh_CN":"<p>其他分站正文</p>"}`).SetIsPublished(true).SaveX(ctx)
	sitemap, err = svc.SitemapXML(ctx, "localhost")
	if err != nil || strings.Contains(sitemap, "/product/691") || strings.Contains(sitemap, "/posts/other-post") {
		t.Fatal("sitemap crossed subsite boundary", err)
	}
	for _, path := range []string{"/product/691", "/posts/other-post"} {
		if response := get("GET", path, "Googlebot"); response.Code != 404 {
			t.Fatalf("foreign page %s returned %d", path, response.Code)
		}
	}
	subctx := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 9})
	sitemap, err = svc.SitemapXML(subctx, "localhost")
	if err != nil || !strings.Contains(sitemap, "/product/691") || strings.Contains(sitemap, "/product/688") {
		t.Fatal("subsite sitemap visibility", err)
	}
	classic := web.NewStorefrontHandler(svc)
	for _, path := range []string{"/", "/product/688", "/posts/使用说明"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("User-Agent", "Googlebot")
		w := httptest.NewRecorder()
		classic.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "<main>") || w.Header().Get("Vary") != "User-Agent" {
			t.Fatalf("Classic crawler fallback failed for %s", path)
		}
	}
	handle.Close()
	if unavailable := get("GET", "/product/688", ""); unavailable.Code != 503 || unavailable.Header().Get("Retry-After") != "60" {
		t.Fatalf("DB failure became %d", unavailable.Code)
	}
}
