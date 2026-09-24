package catalog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/media"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	batch "github.com/NovaWorks/zcard-next/server/internal/data/ent/productcontentbatch"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/sanitize"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
	"golang.org/x/net/html"
	"google.golang.org/protobuf/proto"
)

const batchContentLimit = 1000
const batchDescriptionLimit = 512 * 1024
const batchPreviewBytes = 8 * 1024 * 1024

type contentSnapshot struct {
	ID   uint64
	Hash string
}
type contentBatchPayload struct {
	Patch       *adminv1.ProductContentPatch
	CategoryID  uint64
	Descendants bool
	Categories  map[uint64]uint64
	Targets     []contentSnapshot
}

func batchActor(ctx context.Context) (uint64, error) {
	c := identity.ClaimsFromContext(ctx)
	if c == nil || c.Realm != authn.RealmAdmin || c.Subject == 0 {
		return 0, errors.Unauthorized("catalog.UNAUTHORIZED", "请重新登录管理员账号")
	}
	return c.Subject, nil
}
func contentInvalid(msg string) error { return errors.BadRequest("catalog.BATCH_CONTENT_INVALID", msg) }
func contentStale() error {
	return errors.Conflict("catalog.BATCH_CONTENT_STALE", "商品、类目或内容已发生变化，本次未修改，请重新预览")
}

func normalizeContentPatch(in *adminv1.ProductContentPatch) (*adminv1.ProductContentPatch, error) {
	if in == nil {
		return nil, contentInvalid("请选择修改内容")
	}
	p := proto.Clone(in).(*adminv1.ProductContentPatch)
	if p.CoverAction < 0 || p.CoverAction > 3 || p.ImagesAction < 0 || p.ImagesAction > 2 || p.DescriptionAction < 0 || p.DescriptionAction > 3 {
		return nil, contentInvalid("修改方式无效")
	}
	if p.CoverAction+p.ImagesAction+p.DescriptionAction == 0 {
		return nil, contentInvalid("至少启用一个修改字段")
	}
	if p.CoverAction == 1 {
		if p.Cover == "" || len(p.Cover) > 255 {
			return nil, contentInvalid("请选择有效封面，地址不超过 255 字节")
		}
	} else {
		p.Cover = ""
	}
	if p.ImagesAction == 1 {
		if len(p.Images) == 0 || len(p.Images) > 20 {
			return nil, contentInvalid("请选择 1～20 张详情图片")
		}
		seen := map[string]bool{}
		images := []string{}
		for _, u := range p.Images {
			if u == "" || len(u) > 500 {
				return nil, contentInvalid("详情图片地址无效")
			}
			if !seen[u] {
				seen[u] = true
				images = append(images, u)
			}
		}
		p.Images = images
	} else {
		p.Images = nil
	}
	if p.DescriptionAction == 1 {
		if len(p.Description) > batchDescriptionLimit {
			return nil, contentInvalid("产品介绍不能超过 512 KiB")
		}
		p.Description = sanitize.RichHTML(p.Description)
		if !hasContent(p.Description) {
			return nil, contentInvalid("介绍清洗后为空，请填写内容或明确选择清空")
		}
	} else {
		p.Description = ""
	}
	return p, nil
}
func hasContent(s string) bool {
	doc, _ := html.Parse(strings.NewReader(s))
	if doc == nil {
		return false
	}
	var walk func(*html.Node) bool
	walk = func(n *html.Node) bool {
		if n.Type == html.TextNode && strings.TrimFunc(n.Data, unicode.IsSpace) != "" {
			return true
		}
		if n.Type == html.ElementNode && (n.Data == "img" || n.Data == "video" || n.Data == "source") {
			for _, a := range n.Attr {
				if a.Key == "src" && a.Val != "" {
					return true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if walk(c) {
				return true
			}
		}
		return false
	}
	return walk(doc)
}

// Lock and validate only newly selected assets; existing untouched external URLs remain valid.
func validateContentMedia(ctx context.Context, c *ent.Client, p *adminv1.ProductContentPatch, lock bool) error {
	types := map[string]string{}
	add := func(u, kind string) error {
		if !strings.HasPrefix(u, "/uploads/") || strings.ContainsAny(u, "?#\\") {
			return contentInvalid("请从素材管理选择图片或视频")
		}
		path := strings.TrimPrefix(u, "/uploads/")
		if previous, exists := types[path]; exists && previous != kind {
			return contentInvalid("同一素材不能同时作为图片和视频")
		}
		types[path] = kind
		return nil
	}
	if p.CoverAction == 1 {
		if err := add(p.Cover, "image/"); err != nil {
			return err
		}
	}
	if p.ImagesAction == 1 {
		for _, u := range p.Images {
			if err := add(u, "image/"); err != nil {
				return err
			}
		}
	}
	if p.DescriptionAction == 1 {
		doc, _ := html.Parse(strings.NewReader(p.Description))
		var walk func(*html.Node) error
		walk = func(n *html.Node) error {
			if n.Type == html.ElementNode && (n.Data == "img" || n.Data == "video" || n.Data == "source") {
				for _, a := range n.Attr {
					if a.Key == "src" || a.Key == "poster" {
						kind := "image/"
						if a.Key == "src" && n.Data != "img" {
							kind = "video/"
						}
						if err := add(a.Val, kind); err != nil {
							return err
						}
					}
				}
			}
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				if err := walk(ch); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(doc); err != nil {
			return err
		}
	}
	paths := make([]string, 0, len(types))
	for path := range types {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if lock {
			n, err := c.Media.Update().Where(media.PathEQ(path), media.MimeHasPrefix(types[path])).AddRefCount(0).Save(ctx)
			if err != nil {
				return err
			}
			if n != 1 {
				return contentInvalid("素材已删除或类型不符，请重新选择")
			}
		}
		ok, err := c.Media.Query().Where(media.PathEQ(path), media.MimeHasPrefix(types[path])).Exist(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return contentInvalid("素材已删除或类型不符，请重新选择")
		}
	}
	return nil
}
func contentCategories(ctx context.Context, c *ent.Client, id uint64, desc bool, lock ...bool) (map[uint64]uint64, error) {
	q := c.Category.Query().Where(category.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).Order(ent.Asc(category.FieldID))
	if len(lock) > 0 && lock[0] {
		q.ForUpdate()
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	all := map[uint64]uint64{}
	for _, r := range rows {
		all[r.ID] = r.ParentID
	}
	if _, ok := all[id]; !ok {
		return nil, contentInvalid("请选择本站有效类目")
	}
	out := map[uint64]uint64{id: all[id]}
	if desc {
		for changed := true; changed; {
			changed = false
			for k, parent := range all {
				if _, ok := out[k]; ok {
					continue
				}
				if _, ok := out[parent]; ok {
					out[k] = parent
					changed = true
				}
			}
		}
	}
	return out, nil
}
func contentHash(p *ent.Product, patch *adminv1.ProductContentPatch, categoryMode bool) string {
	v := map[string]any{"id": p.ID, "source": p.UpstreamSourceID}
	if categoryMode {
		v["category"] = p.CategoryID
	}
	if patch.CoverAction != 0 {
		v["cover"] = p.Cover
		v["cover_protected"] = p.CoverProtected
	}
	if patch.ImagesAction != 0 {
		v["images"] = p.Images
	}
	if patch.DescriptionAction != 0 {
		v["description"] = p.Description
		v["description_protected"] = p.DescriptionProtected
	}
	b, _ := json.Marshal(v)
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:])
}
func contentNext(p *ent.Product, patch *adminv1.ProductContentPatch) (*ent.Product, bool, bool) {
	n := *p
	switch patch.CoverAction {
	case 1:
		n.Cover = patch.Cover
		n.CoverProtected = p.UpstreamSourceID > 0
	case 2:
		n.Cover = ""
		n.CoverProtected = p.UpstreamSourceID > 0
	case 3:
		n.CoverProtected = false
	}
	switch patch.ImagesAction {
	case 1:
		n.Images = patch.Images
	case 2:
		n.Images = []string{}
	}
	switch patch.DescriptionAction {
	case 1:
		n.Description = patch.Description
		n.DescriptionProtected = p.UpstreamSourceID > 0
	case 2:
		n.Description = ""
		n.DescriptionProtected = p.UpstreamSourceID > 0
	case 3:
		n.DescriptionProtected = false
	}
	return &n, n.Cover != p.Cover || !slices.Equal(n.Images, p.Images) || n.Description != p.Description, n.CoverProtected != p.CoverProtected || n.DescriptionProtected != p.DescriptionProtected
}
func (s *AdminCatalogService) PreviewBatchUpdateProductContent(ctx context.Context, req *adminv1.PreviewBatchProductContentRequest) (*adminv1.BatchProductContentPreview, error) {
	actor, err := batchActor(ctx)
	if err != nil {
		return nil, err
	}
	patch, err := normalizeContentPatch(req.GetPatch())
	if err != nil {
		return nil, err
	}
	if (len(req.Ids) == 0) == (req.CategoryId == 0) || len(req.Ids) > batchContentLimit {
		return nil, contentInvalid("请选择 1～1000 件商品或一个类目")
	}
	c := data.Client(ctx, s.repo.data)
	if err = validateContentMedia(ctx, c, patch, false); err != nil {
		return nil, err
	}
	payload := contentBatchPayload{Patch: patch, CategoryID: req.CategoryId, Descendants: req.IncludeDescendants == nil || req.GetIncludeDescendants()}
	q := c.Product.Query().Where(product.SubsiteID(tenancy.FromContext(ctx).SubsiteID), product.StatusGTE(0)).Order(ent.Asc(product.FieldID))
	ids := slices.Clone(req.Ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if req.CategoryId > 0 {
		payload.Categories, err = contentCategories(ctx, c, req.CategoryId, payload.Descendants)
		if err != nil {
			return nil, err
		}
		cats := []uint64{}
		for id := range payload.Categories {
			cats = append(cats, id)
		}
		q.Where(product.CategoryIDIn(cats...))
	} else {
		if len(ids) == 0 || ids[0] == 0 {
			return nil, contentInvalid("商品编号无效")
		}
		q.Where(product.IDIn(ids...))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, err
	}
	if req.CategoryId == 0 && total != len(ids) {
		return nil, contentStale()
	}
	skipped, err := q.Clone().Where(product.IsLocked(true)).Count(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := q.Where(product.IsLocked(false)).Limit(batchContentLimit + 1).All(ctx)
	if err != nil {
		return nil, err
	}
	if total == 0 || len(rows) > batchContentLimit {
		return nil, contentInvalid("可修改商品为空或超过 1000 件，请缩小范围")
	}

	encodedPatch, _ := json.Marshal(patch)
	if len(encodedPatch)*len(rows) > batchPreviewBytes {
		return nil, contentInvalid("本批次写入的图文总量超过 8 MiB，请缩小范围")
	}
	out := &adminv1.BatchProductContentPreview{Patch: patch, Matched: int32(total), SkippedLocked: int32(skipped), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}
	bytes := 0
	for _, p := range rows {
		if p.IsLocked {
			out.SkippedLocked++
			continue
		}
		if (patch.CoverAction == 3 || patch.DescriptionAction == 3) && p.UpstreamSourceID == 0 {
			return nil, contentInvalid("恢复跟随上游仅适用于对接商品，请单独选择")
		}
		_, content, protection := contentNext(p, patch)
		changed := content || protection
		payload.Targets = append(payload.Targets, contentSnapshot{p.ID, contentHash(p, patch, req.CategoryId > 0)})
		out.Targets = append(out.Targets, &adminv1.BatchProductContentTarget{Id: p.ID, Name: p.Name, Cover: p.Cover, Images: p.Images, Description: sanitize.RichHTML(p.Description), Upstream: p.UpstreamSourceID > 0, Changed: changed, ContentChanged: content, ProtectionChanged: protection})
		bytes += len(p.Description) + len(p.Cover)
		if bytes > batchPreviewBytes {
			return nil, contentInvalid("目标图文总量超过 8 MiB，请缩小本次范围")
		}
		if changed {
			out.Changed++
		}
		if content {
			out.ContentChanged++
		}
		if protection {
			out.ProtectionChanged++
		}
		if p.UpstreamSourceID > 0 {
			out.UpstreamCount++
		}
	}
	out.Unchanged = out.Matched - out.Changed - out.SkippedLocked
	random := make([]byte, 32)
	if _, err = rand.Read(random); err != nil {
		return nil, err
	}
	out.RequestId = base64.RawURLEncoding.EncodeToString(random)
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	// Only expired previews are disposable. Completed receipts remain queryable.
	if _, err = c.ProductContentBatch.Delete().Where(batch.Completed(false), batch.ExpiresAtLT(time.Now())).Exec(ctx); err != nil {
		return nil, err
	}
	_, err = c.ProductContentBatch.Create().SetToken(out.RequestId).SetActorID(actor).SetSubsiteID(tenancy.FromContext(ctx).SubsiteID).SetPayload(raw).SetExpiresAt(time.Unix(out.ExpiresAt, 0)).SetMatched(out.Matched).SetChanged(out.Changed).SetSkippedLocked(out.SkippedLocked).Save(ctx)
	return out, err
}
func (s *AdminCatalogService) contentBatch(ctx context.Context, token string) (*ent.ProductContentBatch, error) {
	actor, err := batchActor(ctx)
	if err != nil {
		return nil, err
	}
	if len(token) != 43 {
		return nil, contentInvalid("预览编号无效，请重新预览")
	}
	b, err := data.Client(ctx, s.repo.data).ProductContentBatch.Query().Where(batch.TokenEQ(token), batch.ActorID(actor), batch.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("catalog.BATCH_CONTENT_NOT_FOUND", "预览不存在或已过期，请重新预览")
	}
	return b, err
}
func contentResult(b *ent.ProductContentBatch) *adminv1.BatchProductContentResult {
	return &adminv1.BatchProductContentResult{Completed: b.Completed, Matched: b.Matched, Changed: b.Changed, Unchanged: b.Matched - b.Changed - b.SkippedLocked, SkippedLocked: b.SkippedLocked}
}
func (s *AdminCatalogService) GetBatchProductContentResult(ctx context.Context, req *adminv1.BatchProductContentRequest) (*adminv1.BatchProductContentResult, error) {
	b, err := s.contentBatch(ctx, req.GetRequestId())
	if err != nil {
		return nil, err
	}
	return contentResult(b), nil
}
func (s *AdminCatalogService) BatchUpdateProductContent(ctx context.Context, req *adminv1.BatchProductContentRequest) (out *adminv1.BatchProductContentResult, err error) {
	// Authenticate and locate before acquiring the transaction lock.
	found, err := s.contentBatch(ctx, req.GetRequestId())
	if err != nil {
		return nil, err
	}
	err = data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		if err := c.ProductContentBatch.UpdateOneID(found.ID).SetToken(found.Token).Exec(ctx); err != nil {
			return err
		}
		b, err := s.contentBatch(ctx, req.RequestId)
		if err != nil {
			return err
		}
		if b.Completed {
			out = contentResult(b)
			return nil
		}
		if !time.Now().Before(b.ExpiresAt) {
			return contentInvalid("预览已过期，请重新预览")
		}
		var payload contentBatchPayload
		if err = json.Unmarshal(b.Payload, &payload); err != nil {
			return err
		}
		if payload.CategoryID > 0 {
			cats, err := contentCategories(ctx, c, payload.CategoryID, payload.Descendants, s.repo.data.Dialect != db.SQLite)
			if err != nil {
				return err
			}
			if len(cats) != len(payload.Categories) {
				return contentStale()
			}
			for id, parent := range payload.Categories {
				if cur, ok := cats[id]; !ok || cur != parent {
					return contentStale()
				}
			}
			catIDs := []uint64{}
			for id := range cats {
				catIDs = append(catIDs, id)
			}
			slices.Sort(catIDs)
			for _, id := range catIDs {
				if err := c.Category.UpdateOneID(id).AddSort(0).Exec(ctx); err != nil {
					return err
				}
			}
		}
		rows := make([]*ent.Product, 0, len(payload.Targets))
		skipped := b.SkippedLocked
		for _, target := range payload.Targets {
			p, err := data.GuardProductWrite(ctx, s.repo.data, target.ID)
			if data.IsProductLocked(err) {
				skipped++
				continue
			}
			if err != nil {
				return err
			}

			if contentHash(p, payload.Patch, payload.CategoryID > 0) != target.Hash {
				return contentStale()
			}
			rows = append(rows, p)
		}
		if err := validateContentMedia(ctx, c, payload.Patch, true); err != nil {
			return err
		}
		var changed int32
		for _, p := range rows {
			next, content, protection := contentNext(p, payload.Patch)
			if !content && !protection {
				continue
			}
			changed++
			q := c.Product.UpdateOneID(p.ID)
			if payload.Patch.CoverAction != 0 {
				q.SetCover(next.Cover).SetCoverProtected(next.CoverProtected)
			}
			if payload.Patch.ImagesAction != 0 {
				q.SetImages(next.Images)
			}
			if payload.Patch.DescriptionAction != 0 {
				q.SetDescription(next.Description).SetDescriptionProtected(next.DescriptionProtected)
			}
			if err := q.Exec(ctx); err != nil {
				return err
			}
			if err := data.SyncProductMediaRefs(ctx, s.repo.data, p, next); err != nil {
				return err
			}
		}
		// One audit record per committed batch, without storing HTML in the log.
		ids := make([]uint64, 0, len(rows))
		for _, p := range rows {
			ids = append(ids, p.ID)
		}
		if err := c.AuditLog.Create().SetOperatorType("admin").SetOperatorID(b.ActorID).SetPermissionPoint("catalog:write").SetAction("POST").SetRoute("/api/v1/admin/products/batch-content").SetAfter(map[string]any{"request_id": b.Token, "subsite_id": b.SubsiteID, "ids": ids, "matched": b.Matched, "changed": changed, "skipped_locked": skipped, "category_id": payload.CategoryID, "cover_action": payload.Patch.CoverAction, "images_action": payload.Patch.ImagesAction, "description_action": payload.Patch.DescriptionAction}).Exec(ctx); err != nil {
			return err
		}
		b, err = c.ProductContentBatch.UpdateOneID(b.ID).SetCompleted(true).SetChanged(changed).SetSkippedLocked(skipped).SetPayload(json.RawMessage(`{}`)).Save(ctx)
		if err != nil {
			return err
		}
		out = contentResult(b)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
