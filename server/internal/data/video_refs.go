package data

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/media"
	"golang.org/x/net/html"
)

// VideoPaths includes video files and their posters, once per document. JSON
// article content is decoded by the caller so all locales share one reference.
func VideoPaths(content string) map[string]bool {
	out := map[string]bool{}
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return out
	}
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inVideo bool) {
		inVideo = inVideo || n.Type == html.ElementNode && n.Data == "video"
		if inVideo && (n.Data == "video" || n.Data == "source") {
			for _, a := range n.Attr {
				if (a.Key == "src" || a.Key == "poster") && strings.HasPrefix(a.Val, "/uploads/") {
					out[strings.TrimPrefix(a.Val, "/uploads/")] = true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inVideo)
		}
	}
	walk(doc, false)
	return out
}

// SyncVideoRefs runs within the same transaction as the content mutation.
func SyncVideoRefs(ctx context.Context, d *Data, oldContent, newContent string) error {
	old, next := VideoPaths(oldContent), VideoPaths(newContent)
	c := Client(ctx, d)

	paths := make([]string, 0, len(old)+len(next))
	union := map[string]bool{}
	for path := range old {
		union[path] = true
	}
	for path := range next {
		union[path] = true
	}
	for path := range union {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if old[path] == next[path] {
			continue
		}
		q := c.Media.Update().Where(media.PathEQ(path))
		if next[path] {
			n, err := q.AddRefCount(1).Save(ctx)
			if err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("视频或封面已删除，请重新选择素材")
			}
		} else {
			if _, err := q.Where(media.RefCountGT(0)).AddRefCount(-1).Save(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}
