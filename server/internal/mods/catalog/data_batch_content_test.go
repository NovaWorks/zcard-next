package catalog

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productcontentbatch"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"google.golang.org/protobuf/proto"
)

func batchContext() context.Context {
	return identity.WithClaims(context.Background(), &authn.Claims{Subject: 1, Realm: authn.RealmAdmin})
}
func batchImage(t *testing.T, d *data.Data, name string) *ent.Media {
	t.Helper()
	return d.Client.Media.Create().SetName(name).SetPath(name + ".png").SetMime("image/png").SetSize(1).SaveX(context.Background())
}
func previewContent(t *testing.T, s *AdminCatalogService, ctx context.Context, ids []uint64, p *adminv1.ProductContentPatch) *adminv1.BatchProductContentPreview {
	t.Helper()
	v, err := s.PreviewBatchUpdateProductContent(ctx, &adminv1.PreviewBatchProductContentRequest{Ids: ids, Patch: p})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func applyContent(t *testing.T, s *AdminCatalogService, ctx context.Context, v *adminv1.BatchProductContentPreview) *adminv1.BatchProductContentResult {
	t.Helper()
	r, err := s.BatchUpdateProductContent(ctx, &adminv1.BatchProductContentRequest{RequestId: v.RequestId})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestBatchContentAtomicAndIdempotent(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := batchContext()
	m := batchImage(t, d, "shared")
	var ids []uint64
	for i := 0; i < 100; i++ {
		p := d.Client.Product.Create().SetName("product").SetSlug(fmt.Sprint(i)).SetPrice(123).SetFactoryPrice(90).SetPointsRequired(8).SetStatus(0).SetDescription("original").SaveX(ctx)
		ids = append(ids, p.ID)
	}
	patch := &adminv1.ProductContentPatch{CoverAction: 1, Cover: "/uploads/" + m.Path, ImagesAction: 1, Images: []string{"/uploads/" + m.Path}, DescriptionAction: 1, Description: `<p>介绍</p><img src="/uploads/shared.png"><script>alert(1)</script>`}
	v := previewContent(t, s, ctx, ids, patch)
	if v.Matched != 100 || v.Changed != 100 || strings.Contains(v.Patch.Description, "script") {
		t.Fatalf("bad preview %v", v)
	}
	if d.Client.Media.GetX(ctx, m.ID).RefCount != 0 || d.Client.Product.GetX(ctx, ids[0]).Cover != "" {
		t.Fatal("preview mutated data")
	}
	if got := applyContent(t, s, ctx, v); !got.Completed || got.Changed != 100 {
		t.Fatalf("result %v", got)
	}
	if d.Client.Media.GetX(ctx, m.ID).RefCount != 100 {
		t.Fatal("shared media delta collapsed")
	}
	for i := 0; i < 2; i++ {
		applyContent(t, s, ctx, v)
	}
	if d.Client.AuditLog.Query().CountX(ctx) != 1 || d.Client.Media.GetX(ctx, m.ID).RefCount != 100 {
		t.Fatal("duplicate side effects")
	}
	p := d.Client.Product.GetX(ctx, ids[0])
	if p.Price != 123 || p.FactoryPrice != 90 || p.PointsRequired != 8 || p.Status != 0 {
		t.Fatal("unrelated fields changed")
	}
	// Clearing one field keeps references from the other fields.
	applyContent(t, s, ctx, previewContent(t, s, ctx, ids[:30], &adminv1.ProductContentPatch{CoverAction: 2}))
	if d.Client.Media.GetX(ctx, m.ID).RefCount != 100 {
		t.Fatal("lost retained field references")
	}
	applyContent(t, s, ctx, previewContent(t, s, ctx, ids[:30], &adminv1.ProductContentPatch{ImagesAction: 2, DescriptionAction: 2}))
	if d.Client.Media.GetX(ctx, m.ID).RefCount != 70 {
		t.Fatal("incorrect release count")
	}
	// A reconstructed service can retrieve the durable receipt.
	restored := NewAdminCatalogService(NewProductRepoImpl(d, nil), nil, nil, nil)
	result, err := restored.GetBatchProductContentResult(ctx, &adminv1.BatchProductContentRequest{RequestId: v.RequestId})
	if err != nil || !result.Completed {
		t.Fatalf("receipt %v %v", result, err)
	}
}
func TestBatchContentScopeAndConflicts(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := batchContext()
	root := d.Client.Category.Create().SetName("root").SaveX(ctx)
	child := d.Client.Category.Create().SetName("child").SetParentID(root.ID).SaveX(ctx)
	a := d.Client.Product.Create().SetName("A").SetSlug("a").SetCategoryID(root.ID).SetDescription("a").SaveX(ctx)
	b := d.Client.Product.Create().SetName("B").SetSlug("b").SetCategoryID(child.ID).SetDescription("b").SaveX(ctx)
	d.Client.Product.Create().SetName("deleted").SetSlug("deleted").SetCategoryID(root.ID).SetStatus(-1).SaveX(ctx)
	foreign := d.Client.Product.Create().SetName("foreign").SetSlug("foreign").SetCategoryID(root.ID).SetSubsiteID(2).SaveX(ctx)
	req := &adminv1.PreviewBatchProductContentRequest{CategoryId: root.ID, Patch: &adminv1.ProductContentPatch{DescriptionAction: 1, Description: "new"}}
	v, err := s.PreviewBatchUpdateProductContent(ctx, req)
	if err != nil || v.Matched != 2 {
		t.Fatalf("category %v %v", v, err)
	}
	req.IncludeDescendants = proto.Bool(false)
	direct, err := s.PreviewBatchUpdateProductContent(ctx, req)
	if err != nil || direct.Matched != 1 {
		t.Fatal("direct category")
	}
	d.Client.Product.UpdateOneID(b.ID).SetDescription("concurrent").ExecX(ctx)
	if _, err = s.BatchUpdateProductContent(ctx, &adminv1.BatchProductContentRequest{RequestId: v.RequestId}); err == nil {
		t.Fatal("stale content accepted")
	}
	if d.Client.Product.GetX(ctx, a.ID).Description != "a" {
		t.Fatal("partial write")
	}
	// A price update does not invalidate a description-only preview.
	d.Client.Product.UpdateOneID(a.ID).SetPrice(999).ExecX(ctx)
	applyContent(t, s, ctx, direct)
	if d.Client.Product.GetX(ctx, a.ID).Price != 999 {
		t.Fatal("price overwritten")
	}
	for _, ids := range [][]uint64{{a.ID, foreign.ID}, {a.ID, 999999}, {0}} {
		if _, err = s.PreviewBatchUpdateProductContent(ctx, &adminv1.PreviewBatchProductContentRequest{Ids: ids, Patch: req.Patch}); err == nil {
			t.Fatal("invalid scope accepted")
		}
	}
	other := identity.WithClaims(ctx, &authn.Claims{Subject: 2, Realm: authn.RealmAdmin})
	if _, err = s.GetBatchProductContentResult(other, &adminv1.BatchProductContentRequest{RequestId: direct.RequestId}); err == nil {
		t.Fatal("other actor receipt")
	}
	if _, err = s.GetBatchProductContentResult(tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 2}), &adminv1.BatchProductContentRequest{RequestId: direct.RequestId}); err == nil {
		t.Fatal("other site receipt")
	}
	if _, err = s.PreviewBatchUpdateProductContent(context.Background(), req); err == nil {
		t.Fatal("anonymous preview")
	}
}
func TestBatchContentRollbackOnMediaError(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := batchContext()
	m := batchImage(t, d, "rollback")
	a := d.Client.Product.Create().SetName("A").SetSlug("a").SaveX(ctx)
	b := d.Client.Product.Create().SetName("B").SetSlug("b").SaveX(ctx)
	v := previewContent(t, s, ctx, []uint64{a.ID, b.ID}, &adminv1.ProductContentPatch{CoverAction: 1, Cover: "/uploads/" + m.Path})
	calls := 0
	d.Client.Media.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			calls++
			if calls == 3 {
				return nil, fmt.Errorf("injected ref failure")
			}
			return next.Mutate(ctx, m)
		})
	})
	if _, err := s.BatchUpdateProductContent(ctx, &adminv1.BatchProductContentRequest{RequestId: v.RequestId}); err == nil {
		t.Fatal("injected failure ignored")
	}
	if d.Client.Product.GetX(ctx, a.ID).Cover != "" || d.Client.Product.GetX(ctx, b.ID).Cover != "" || d.Client.Media.GetX(ctx, m.ID).RefCount != 0 || d.Client.AuditLog.Query().CountX(ctx) != 0 {
		t.Fatal("rollback incomplete")
	}
}
func TestBatchContentValidationAndExpiry(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := batchContext()
	p := d.Client.Product.Create().SetName("A").SetSlug("a").SaveX(ctx)
	for _, patch := range []*adminv1.ProductContentPatch{{}, {CoverAction: 1}, {ImagesAction: 1}, {DescriptionAction: 1, Description: "<p>&nbsp;</p>"}, {DescriptionAction: 1, Description: "<script>alert(1)</script>"}, {CoverAction: 1, Cover: "https://outside/image.png"}, {CoverAction: 1, Cover: "/uploads/missing.png"}, {CoverAction: 4}, {ImagesAction: 3}, {CoverAction: 3}, {DescriptionAction: 1, Description: strings.Repeat("x", batchDescriptionLimit+1)}} {
		if _, err := s.PreviewBatchUpdateProductContent(ctx, &adminv1.PreviewBatchProductContentRequest{Ids: []uint64{p.ID}, Patch: patch}); err == nil {
			t.Fatalf("invalid accepted %v", patch)
		}
	}
	v := previewContent(t, s, ctx, []uint64{p.ID}, &adminv1.ProductContentPatch{DescriptionAction: 2})
	d.Client.ProductContentBatch.Update().Where(productcontentbatch.TokenEQ(v.RequestId)).SetExpiresAt(time.Now().Add(-time.Minute)).ExecX(ctx)
	if _, err := s.BatchUpdateProductContent(ctx, &adminv1.BatchProductContentRequest{RequestId: v.RequestId}); err == nil {
		t.Fatal("expired accepted")
	}
}
func TestBatchContentUpstreamProtection(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := batchContext()
	m := batchImage(t, d, "local")
	p := d.Client.Product.Create().SetName("A").SetSlug("a").SetUpstreamSourceID(1).SetUpstreamProductCode("a").SaveX(ctx)
	v := previewContent(t, s, ctx, []uint64{p.ID}, &adminv1.ProductContentPatch{CoverAction: 1, Cover: "/uploads/" + m.Path, DescriptionAction: 2})
	applyContent(t, s, ctx, v)
	row := d.Client.Product.GetX(ctx, p.ID)
	if !row.CoverProtected || !row.DescriptionProtected {
		t.Fatal("protection not stored")
	}
	input := port.UpstreamProductInput{ConnectionID: 1, UpstreamProductCode: "a", Name: "upstream", Price: 234, Status: 1, Description: "upstream description", DescriptionSet: true, Cover: "https://upstream/image.png"}
	if _, _, err := s.repo.UpsertUpstreamProduct(ctx, input); err != nil {
		t.Fatal(err)
	}
	row = d.Client.Product.GetX(ctx, p.ID)
	if row.Cover != "/uploads/"+m.Path || row.Description != "" || row.Price != 234 {
		t.Fatal("upstream overwrote protected content or price blocked")
	}
	applyContent(t, s, ctx, previewContent(t, s, ctx, []uint64{p.ID}, &adminv1.ProductContentPatch{CoverAction: 3, DescriptionAction: 3}))
	if _, _, err := s.repo.UpsertUpstreamProduct(ctx, input); err != nil {
		t.Fatal(err)
	}
	row = d.Client.Product.GetX(ctx, p.ID)
	if row.Cover != input.Cover || row.Description != input.Description || d.Client.Media.GetX(ctx, m.ID).RefCount != 0 {
		t.Fatal("follow upstream failed")
	}
	input.Description = ""
	if _, _, err := s.repo.UpsertUpstreamProduct(ctx, input); err != nil {
		t.Fatal(err)
	}
	if d.Client.Product.GetX(ctx, p.ID).Description != "" {
		t.Fatal("explicit upstream empty description ignored")
	}
}
func TestProductMediaReferenceUpgradeAndLifecycle(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := batchContext()
	m := batchImage(t, d, "history")
	p := d.Client.Product.Create().SetName("history").SetSlug("history").SetStatus(0).SetCover("/uploads/history.png").SetDescription(`<img src="/uploads/history.png">`).SaveX(ctx)
	if err := data.UpgradeProductMediaRefs(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := data.UpgradeProductMediaRefs(ctx, d); err != nil {
		t.Fatal(err)
	}
	if d.Client.Media.GetX(ctx, m.ID).RefCount != 1 {
		t.Fatal("historical reference rebuild")
	}
	if _, err := s.DeleteProduct(ctx, &adminv1.DeleteProductRequest{Id: p.ID}); err != nil {
		t.Fatal(err)
	}
	if d.Client.Media.GetX(ctx, m.ID).RefCount != 0 {
		t.Fatal("delete reference not released")
	}
}
func TestBatchContentThousandProducts(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := batchContext()
	var ids []uint64
	for i := 0; i < 1001; i++ {
		p := d.Client.Product.Create().SetName("bulk").SetSlug(fmt.Sprint(i)).SaveX(ctx)
		ids = append(ids, p.ID)
	}
	patch := &adminv1.ProductContentPatch{DescriptionAction: 1, Description: strings.Repeat("description", 100)}
	if _, err := s.PreviewBatchUpdateProductContent(ctx, &adminv1.PreviewBatchProductContentRequest{Ids: ids, Patch: patch}); err == nil {
		t.Fatal("1001 accepted")
	}
	start := time.Now()
	v := previewContent(t, s, ctx, ids[:1000], patch)
	r := applyContent(t, s, ctx, v)
	if r.Changed != 1000 {
		t.Fatal("1000 truncated")
	}
	t.Logf("1000 products preview+apply: %s", time.Since(start))
}
