// Package seo 搜索引擎优化（SEO）基础：robots.txt + sitemap.xml 动态生成。
//
//   - robots.txt：全站放行 + 站点地址指向 sitemap；可经设置 site.robots_custom 追加规则
//   - sitemap.xml：首页/商品列表/文章列表/分类页 + 上架商品详情 + 已发布文章详情
//     （lastmod 取 updated_at/published_at），URL 基准取设置 site.url，未配置回落请求 Host
//
// 页面级 SEO（canonical/meta/JSON-LD）由 storefront 消费公开配置生成，本模块不涉页面。
package seo
