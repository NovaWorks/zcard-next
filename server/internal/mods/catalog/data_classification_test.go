package catalog

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"testing"
)

func TestClassifyExistingPreviewConflictAndProtected(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := context.Background()
	cat := d.Client.Category.Create().SetName("Apple").SaveX(ctx)
	a := d.Client.Product.Create().SetName("小火箭 A").SetSlug("a").SaveX(ctx)
	b := d.Client.Product.Create().SetName("小火箭 B").SetSlug("b").SaveX(ctx)
	d.Client.Product.Create().SetName("小火箭 locked").SetSlug("l").SetIsLocked(true).SaveX(ctx)
	d.Client.Product.Create().SetName("小火箭 manual").SetSlug("m").SetCategoryProtected(true).SaveX(ctx)
	req := &adminv1.ClassifyProductsRequest{Keywords: []string{"小火箭"}, CategoryId: cat.ID}
	preview, e := s.ClassifyProducts(ctx, req)
	if e != nil || len(preview.Items) != 2 || preview.SkippedLocked != 1 || preview.SkippedProtected != 1 {
		t.Fatalf("preview=%v %v", preview, e)
	}
	if d.Client.Product.GetX(ctx, a.ID).CategoryID != 0 {
		t.Fatal("preview wrote category")
	}
	req.Apply = true
	req.Revisions = map[uint64]string{}
	for _, p := range preview.Items {
		req.Revisions[p.Id] = p.Revision
	}
	d.Client.Product.UpdateOneID(b.ID).SetName("运营已修改名称").ExecX(ctx)
	d.Client.Product.Create().SetName("小火箭 new").SetSlug("new").SaveX(ctx)
	got, e := s.ClassifyProducts(ctx, req)
	if e != nil || got.Updated != 1 {
		t.Fatalf("apply=%v %v", got, e)
	}
	if d.Client.Product.GetX(ctx, a.ID).CategoryID != cat.ID || !d.Client.Product.GetX(ctx, a.ID).CategoryProtected {
		t.Fatal("category not protected")
	}
	if d.Client.Product.GetX(ctx, b.ID).CategoryID != 0 {
		t.Fatal("conflicting product overwritten")
	}
}
