package seo

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	kerrors "github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

var privateTitles = map[string]string{
	"lottery": "幸运抽奖",
	"member":  "个人中心", "order": "订单详情", "payment": "订单支付", "fetch": "订单查询",
	"tickets": "售后工单", "withdraw": "申请提现", "affiliate": "推广中心", "cart": "购物车",
	"coupons": "优惠券", "install": "安装向导", "login": "登录", "register": "注册", "forgot-password": "找回密码",
}

func (s *SeoService) publicProduct(ctx context.Context, id uint64) (*ProductSEO, error) {
	// Reuse the storefront service: visibility, fulfillment stock and subsite pricing must agree.
	p, err := s.catalog.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: id})
	if err != nil {
		if kerrors.Code(err) == 404 {
			return nil, nil
		}
		return nil, err
	}
	return storefrontProductSEO(p, time.Now().Unix()), nil
}

// Apply the same public offer rules as the theme: expiry, quota, and SKU prices.
func storefrontProductSEO(p *storefrontv1.Product, now int64) *ProductSEO {
	result := &ProductSEO{ID: p.Id, Name: p.Name, DescriptionHTML: p.Description, Cover: p.Cover, PriceCents: p.PriceCents, Stock: p.Stock}
	offer := func(id uint64, price int64, flash *storefrontv1.FlashOffer) ProductOfferSEO {
		stock := p.Stock
		if flash != nil && flash.EndAt > now {
			if flash.Remaining <= 0 {
				stock = 0
			} else if flash.PriceCents > 0 && flash.PriceCents < price {
				price = flash.PriceCents
			}
		}
		return ProductOfferSEO{SKU: id, PriceCents: price, Stock: stock}
	}
	for _, sku := range p.Skus {
		if sku != nil {
			result.Offers = append(result.Offers, offer(sku.Id, sku.PriceCents, sku.FlashSale))
		}
	}
	if len(result.Offers) == 0 {
		result.Offers = []ProductOfferSEO{offer(0, p.PriceCents, p.FlashSale)}
	}
	result.PriceCents = result.Offers[0].PriceCents
	result.Stock = result.Offers[0].Stock
	return result
}

func basicPage(site siteInfo, host, path, title string) seoPageData {
	base := site.base(host)
	d := seoPageData{Site: site, Base: base, Canonical: base + path, OGType: "website", OGImage: absoluteURL(site.Logo, base),
		Keywords: strings.Join(nonEmpty(site.SeoKeywords, site.Name), ","), Robots: "index,follow",
		Description: truncateStr(stripTags(orDefaultStr(site.SeoDesc, site.Name+"数字商品商店。浏览商品、查看使用说明与交付信息。")), 150),
	}
	d.Title = title + " - " + site.Name
	if title == "" {
		d.Title = orDefaultStr(site.SeoTitle, site.Name)
	}
	org := organization(site, base)
	org["@context"] = "https://schema.org"
	ld := []any{org}
	if title != "" {
		ld = append(ld, map[string]any{"@context": "https://schema.org", "@type": "CollectionPage", "name": d.Title, "description": d.Description, "url": d.Canonical})
		ld = append(ld, map[string]any{"@context": "https://schema.org", "@type": "BreadcrumbList", "itemListElement": []any{
			map[string]any{"@type": "ListItem", "position": 1, "name": site.Name, "item": base + "/"},
			map[string]any{"@type": "ListItem", "position": 2, "name": title, "item": d.Canonical},
		}})
	}
	d.JSONLD = jsonldOf(ld)
	d.Body = template.HTML("<h1>" + template.HTMLEscapeString(orDefaultStr(title, site.Name)) + "</h1>")
	return d
}

func noindexPage(site siteInfo, host, path, title string) seoPageData {
	d := basicPage(site, host, path, title)
	d.Robots = "noindex,nofollow"
	d.JSONLD = ""
	return d
}

// pageData covers theme business routes, never API/assets. Unknown pages are real 404s.
func (s *SeoService) pageData(r *http.Request) (seoPageData, int, error) {
	site := s.loadSite(r.Context())
	path := strings.TrimRight(r.URL.Path, "/")
	if path == "" || path == "/index.html" {
		path = "/"
	}
	fail := func() (seoPageData, int, error) { return noindexPage(site, r.Host, path, "页面不存在"), 404, nil }
	segment := strings.Split(strings.TrimPrefix(path, "/"), "/")[0]
	if title, ok := privateTitles[segment]; ok {
		if segment == "lottery" && r.URL.Query().Get("tab") == "prizes" {
			title = "我的奖品"
		}
		return noindexPage(site, r.Host, path, title), 200, nil
	}
	if strings.HasPrefix(path, "/product/") {
		raw := strings.TrimPrefix(path, "/product/")
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 {
			return fail()
		}
		p, err := s.publicProduct(r.Context(), id)
		if err != nil {
			return seoPageData{}, 503, err
		}
		if p == nil {
			return fail()
		}
		return productPageData(site, r.Host, p), 200, nil
	}
	if strings.HasPrefix(path, "/posts/") {
		slug := strings.TrimPrefix(path, "/posts/")
		if slug == "" || strings.Contains(slug, "/") {
			return fail()
		}
		p, err := s.repo.GetPostSEO(r.Context(), slug)
		if err != nil {
			return seoPageData{}, 503, err
		}
		if p == nil {
			return fail()
		}
		return postPageData(site, r.Host, p), 200, nil
	}
	title, ok := map[string]string{"/": "", "/products": "全部商品", "/posts": "文章公告", "/points": "积分商城"}[path]
	if !ok {
		return fail()
	}
	q := r.URL.Query()
	if (path == "/products" || path == "/") && q.Get("category_id") != "" {
		id, _ := strconv.ParseUint(q.Get("category_id"), 10, 64)
		if id > 0 {
			cats, err := s.catalog.ListCategories(r.Context(), &emptypb.Empty{})
			if err != nil {
				return seoPageData{}, 503, err
			}
			found := false
			for _, c := range cats.Categories {
				if c.Id == id {
					title = c.Name
					found = true
					break
				}
			}
			if !found {
				return fail()
			}
		}
	}
	d := basicPage(site, r.Host, path, title)
	if q.Get("keyword") != "" {
		d.Robots = "noindex,nofollow"
		d.JSONLD = ""
	}
	body, err := s.listBody(r, path, site)
	if err != nil {
		return seoPageData{}, 503, err
	}
	d.Body += template.HTML(body)
	return d, 200, nil
}

func positiveInt(raw string, fallback, max int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return fallback
	}
	if n > max {
		return max
	}
	return n
}

func (s *SeoService) listBody(r *http.Request, path string, site siteInfo) (string, error) {
	q := r.URL.Query()
	page := positiveInt(q.Get("page"), 1, 100000)
	size := positiveInt(q.Get("page_size"), 20, 60)
	base := site.base(r.Host)
	var body strings.Builder
	body.WriteString(`<nav><a href="` + base + `/products">全部商品</a> · <a href="` + base + `/posts">文章公告</a> · <a href="` + base + `/points">积分商城</a></nav><ul>`)
	var total int64
	if path == "/posts" {
		category, _ := strconv.ParseUint(q.Get("category_id"), 10, 64)
		rows, count, err := s.repo.ListPostSEO(r.Context(), q.Get("type"), category, page, size)
		if err != nil {
			return "", err
		}
		total = count
		for _, p := range rows {
			body.WriteString(`<li><a href="` + template.HTMLEscapeString(base+"/posts/"+url.PathEscape(p.Slug)) + `">` + template.HTMLEscapeString(p.Title) + `</a></li>`)
		}
	} else {
		category, _ := strconv.ParseUint(q.Get("category_id"), 10, 64)
		rows, err := s.catalog.ListProducts(r.Context(), &storefrontv1.ListProductsRequest{Page: int32(page), PageSize: int32(size), CategoryId: category, Keyword: q.Get("keyword"), Sort: q.Get("sort"), PointsOnly: path == "/points"})
		if err != nil {
			return "", err
		}
		total = rows.Total
		for _, p := range rows.Items {
			price := orDefaultStr(site.Currency, "CNY") + " " + priceYuan(storefrontProductSEO(p, time.Now().Unix()).PriceCents)
			if path == "/points" {
				price = strconv.FormatInt(p.PointsRequired, 10) + " 积分"
			}
			body.WriteString(`<li><a href="` + base + "/product/" + u64str(p.Id) + `">` + template.HTMLEscapeString(p.Name) + `</a> ` + template.HTMLEscapeString(price) + `</li>`)
		}
	}
	body.WriteString("</ul>")
	if int64(page*size) < total {
		q.Set("page", strconv.Itoa(page+1))
		body.WriteString(`<a href="` + template.HTMLEscapeString(path+"?"+q.Encode()) + `">下一页</a>`)
	}
	return body.String(), nil
}
