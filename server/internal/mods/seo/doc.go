// Package seo 搜索引擎优化（SEO）：robots.txt/sitemap + 爬虫动态渲染。
//
//   - robots.txt：私有页禁抓 + 自定义规则（site.robots_custom）+ Sitemap 指向
//   - sitemap.xml：首页/列表/上架商品/已发布文章（lastmod），URL 基准 site.url
//   - 动态渲染（Dynamic Rendering，Google 认可模式）：爬虫 UA 请求商品/文章
//     详情时实时从 DB 渲染完整 SEO HTML（title/canonical/og/JSON-LD + 正文）——
//     内容永远新鲜、删除即真 404，零重建零重启；真人请求走静态页/SPA 不变
//
// 页面级 SEO（canonical/meta/JSON-LD）由 storefront 消费公开配置生成，本模块不涉页面。
package seo
