package data

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/media"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	"golang.org/x/net/html"
)

// ProductMediaPaths counts a library asset once per product, across all fields.
func ProductMediaPaths(p *ent.Product) map[string]bool {
	out := map[string]bool{}
	if p == nil {
		return out
	}
	add := func(u string) {
		if strings.HasPrefix(u, "/uploads/") {
			out[strings.TrimPrefix(u, "/uploads/")] = true
		}
	}
	add(p.Cover)
	for _, u := range p.Images {
		add(u)
	}
	doc, _ := html.Parse(strings.NewReader(p.Description))
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "img" || n.Data == "video" || n.Data == "source") {
			for _, a := range n.Attr {
				if a.Key == "src" || a.Key == "poster" {
					add(a.Val)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	if doc != nil {
		walk(doc)
	}
	return out
}

// SyncProductMediaRefs participates in the product transaction. Existing external
// and harvested images are not library assets. Batch input is validated separately.
func SyncProductMediaRefs(ctx context.Context, d *Data, old, next *ent.Product) error {
	before, after := ProductMediaPaths(old), ProductMediaPaths(next)
	union := map[string]bool{}
	for p := range before {
		union[p] = true
	}
	for p := range after {
		union[p] = true
	}
	paths := make([]string, 0, len(union))
	for p := range union {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if before[p] == after[p] {
			continue
		}
		q := Client(ctx, d).Media.Update().Where(media.PathEQ(p))
		if after[p] {
			if _, err := q.AddRefCount(1).Save(ctx); err != nil {
				return err
			}
		} else {
			if _, err := q.Where(media.RefCountGT(0)).AddRefCount(-1).Save(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// UpgradeProductMediaRefs is an idempotent data migration run before serving
// requests. Rebuild known reference owners so old description images are counted.
func UpgradeProductMediaRefs(ctx context.Context, d *Data) error {
	return Tx(ctx, d, func(ctx context.Context) error {
		c := Client(ctx, d)
		const group = "internal_media"
		const key = "product_content_refs_v1"
		if err := c.Setting.Create().SetGroup(group).SetKey(key).SetValue(json.RawMessage(`false`)).OnConflictColumns(setting.FieldGroup, setting.FieldKey).Ignore().Exec(ctx); err != nil {
			return err
		}
		if _, err := c.Setting.Update().Where(setting.GroupEQ(group), setting.KeyEQ(key)).SetKey(key).Save(ctx); err != nil {
			return err
		}
		marker, err := c.Setting.Query().Where(setting.GroupEQ(group), setting.KeyEQ(key)).Only(ctx)
		if err != nil {
			return err
		}
		if string(marker.Value) == "true" {
			return nil
		}
		rows, err := c.Media.Query().All(ctx)
		if err != nil {
			return err
		}
		counts := map[uint64]int32{}
		byPath := map[string]uint64{}
		for _, m := range rows {
			byPath[m.Path] = m.ID
		}
		add := func(paths map[string]bool) {
			for p := range paths {
				if id := byPath[p]; id != 0 {
					counts[id]++
				}
			}
		}
		var cursor uint64
		for {
			products, err := c.Product.Query().Where(product.IDGT(cursor), product.StatusGTE(0)).Order(ent.Asc(product.FieldID)).Limit(500).All(ctx)
			if err != nil {
				return err
			}
			if len(products) == 0 {
				break
			}
			for _, p := range products {
				add(ProductMediaPaths(p))
				cursor = p.ID
			}
		}
		banners, err := c.Banner.Query().All(ctx)
		if err != nil {
			return err
		}
		for _, b := range banners {
			add(ProductMediaPaths(&ent.Product{Cover: b.Image, Images: []string{b.MobileImage}}))
		}
		posts, err := c.Post.Query().All(ctx)
		if err != nil {
			return err
		}
		for _, p := range posts {
			var locales map[string]string
			if json.Unmarshal([]byte(p.ContentJSON), &locales) != nil {
				continue
			}
			var content strings.Builder
			for _, v := range locales {
				content.WriteString(v)
			}
			add(VideoPaths(content.String()))
		}
		messages, err := c.TicketMessage.Query().All(ctx)
		if err != nil {
			return err
		}
		for _, m := range messages {
			seen := map[uint64]bool{}
			for _, id := range m.Attachments {
				if !seen[id] {
					counts[id]++
					seen[id] = true
				}
			}
		}
		for _, m := range rows {
			if err := c.Media.UpdateOneID(m.ID).SetRefCount(counts[m.ID]).Exec(ctx); err != nil {
				return fmt.Errorf("重建素材引用: %w", err)
			}
		}
		return c.Setting.UpdateOneID(marker.ID).SetValue(json.RawMessage(`true`)).Exec(ctx)
	})
}
