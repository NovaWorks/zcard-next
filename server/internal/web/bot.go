// Package web：爬虫动态渲染接口（fullstack/非 fullstack 共享定义）。
package web

import (
	"net/http"
	"regexp"
)

// BotRenderer 爬虫动态渲染（seo mod 实现；nil 时跳过）：
// 命中 SEO 路由时向 w 写完整响应（含真 404）并返回 true。
type BotRenderer interface {
	TryRenderBot(w http.ResponseWriter, r *http.Request) bool
}

// botUARe 搜索引擎爬虫 UA（主流收录方；命中即走动态渲染——
// Classic 使用专用正文回退；支持 server-v1 的主题对所有 UA 提供相同元数据）。
var botUARe = regexp.MustCompile(`(?i)bot|spider|slurp|crawl`)

func isBotRequest(r *http.Request) bool {
	return botUARe.MatchString(r.UserAgent())
}
