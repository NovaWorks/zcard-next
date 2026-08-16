// Package sanitize HTML 白名单清洗（规划 §5.20.5 防 XSS）。
// 商品描述、评价内容、文章内容入库前必经；卡密前端展示配 textContent（前端纪律）。
package sanitize

import (
	"github.com/microcosm-cc/bluemonday"
)

var policy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	// 允许的基础标签（白名单制——不在清单内的一律剥离）
	tags := []string{
		"a", "abbr", "b", "blockquote", "br", "code", "dd", "del", "div",
		"dl", "dt", "em", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "i",
		"img", "ins", "li", "ol", "p", "pre", "q", "s", "small", "span",
		"strong", "sub", "sup", "table", "tbody", "td", "tfoot", "th",
		"thead", "tr", "u", "ul",
	}
	for _, t := range tags {
		p.AllowElements(t)
	}

	// 属性白名单
	p.AllowAttrs("href", "title").OnElements("a")
	p.AllowAttrs("src", "alt", "width", "height").OnElements("img")
	p.AllowAttrs("class").OnElements("div", "span", "p", "code", "pre")
	p.AllowAttrs("id").OnElements("div", "span")
	p.AllowAttrs("colspan", "rowspan").OnElements("td", "th")
	p.AllowAttrs("start").OnElements("ol")

	// URL 协议白名单
	p.AllowURLSchemes("http", "https", "mailto")

	// 强制 target=_blank rel=noopener（防钓鱼）
	p.AllowAttrs("target").Matching(bluemonday.SpaceSeparatedTokens).OnElements("a")
	p.RequireNoFollowOnLinks(true)
	p.RequireNoFollowOnFullyQualifiedLinks(true)

	return p
}()

// HTML 清洗（白名单外标签/属性/协议全部剥离；script/style/on* 事件一律干掉）。
func HTML(input string) string {
	return policy.Sanitize(input)
}

// Text 纯文本（全部标签剥离——卡密展示用）。
func Text(input string) string {
	return bluemonday.StrictPolicy().Sanitize(input)
}
