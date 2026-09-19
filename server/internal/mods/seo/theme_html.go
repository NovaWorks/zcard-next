package seo

import (
	"bytes"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"net/http"
	"strings"
)

// RenderThemeHTML provides the same live metadata to browsers and crawlers.
func (s *SeoService) RenderThemeHTML(r *http.Request, shell []byte) ([]byte, int, error) {
	d, status, err := s.pageData(r)
	if err != nil {
		d = noindexPage(s.loadSite(r.Context()), r.Host, r.URL.Path, "页面暂时无法加载")
		status = http.StatusServiceUnavailable
	}
	out, err := injectThemeHTML(shell, d)
	return out, status, err
}
func seoHeadNode(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if n.Data == "title" {
		return true
	}
	attrs := map[string]string{}
	for _, a := range n.Attr {
		attrs[a.Key] = a.Val
	}
	if n.Data == "link" {
		return attrs["rel"] == "canonical"
	}
	if n.Data == "script" {
		return attrs["type"] == "application/ld+json"
	}
	if n.Data != "meta" {
		return false
	}
	key := attrs["name"]
	if key == "" {
		key = attrs["property"]
	}
	return strings.HasPrefix(key, "og:") || strings.HasPrefix(key, "twitter:") || key == "description" || key == "keywords" || key == "robots" || key == "google-site-verification" || key == "msvalidate.01"
}
func findElement(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findElement(c, tag); found != nil {
			return found
		}
	}
	return nil
}
func injectThemeHTML(shell []byte, d seoPageData) ([]byte, error) {
	doc, err := html.Parse(bytes.NewReader(shell))
	if err != nil {
		return nil, err
	}
	rendered, err := renderPage(d)
	if err != nil {
		return nil, err
	}
	source, err := html.Parse(strings.NewReader(rendered))
	if err != nil {
		return nil, err
	}
	head := findElement(doc, "head")
	sourceHead := findElement(source, "head")
	for c := head.FirstChild; c != nil; {
		next := c.NextSibling
		if seoHeadNode(c) {
			head.RemoveChild(c)
		}
		c = next
	}
	for c := sourceHead.FirstChild; c != nil; {
		next := c.NextSibling
		if seoHeadNode(c) {
			sourceHead.RemoveChild(c)
			c.Attr = append(c.Attr, html.Attribute{Key: "data-zcard-seo", Val: ""})
			head.AppendChild(c)
		}
		c = next
	}
	fallback := &html.Node{Type: html.ElementNode, DataAtom: atom.Noscript, Data: "noscript", Attr: []html.Attribute{{Key: "data-zcard-seo", Val: ""}}}
	main := findElement(source, "main")
	main.Parent.RemoveChild(main)
	fallback.AppendChild(main)
	findElement(doc, "body").AppendChild(fallback)
	var out bytes.Buffer
	err = html.Render(&out, doc)
	return out.Bytes(), err
}
