package catalog

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"testing"
)

func TestBatchCategoryAtomicAndScoped(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := context.Background()
	old := d.Client.Category.Create().SetName("原分类").SaveX(ctx)
	target := d.Client.Category.Create().SetName("隐藏目标").SetHide(true).SaveX(ctx)
	foreign := d.Client.Category.Create().SetSubsiteID(2).SetName("其他站").SaveX(ctx)
	a := d.Client.Product.Create().SetName("A").SetSlug("a").SetPrice(123).SetStatus(1).SetCategoryID(old.ID).SaveX(ctx)
	b := d.Client.Product.Create().SetName("B").SetSlug("b").SetPrice(456).SetStatus(0).SetCategoryID(old.ID).SaveX(ctx)
	other := d.Client.Product.Create().SetSubsiteID(2).SetName("other").SetSlug("other").SaveX(ctx)
	archived := d.Client.Product.Create().SetName("deleted").SetSlug("deleted").SetStatus(deletedProductStatus).SaveX(ctx)
	for _, req := range []*adminv1.BatchUpdateProductCategoryRequest{
		{Ids: []uint64{a.ID, 999999}, CategoryId: target.ID},
		{Ids: []uint64{a.ID, other.ID}, CategoryId: target.ID},
		{Ids: []uint64{a.ID, archived.ID}, CategoryId: target.ID},
		{Ids: []uint64{a.ID}, CategoryId: foreign.ID},
		{Ids: []uint64{a.ID}, CategoryId: 999999},
		{Ids: []uint64{a.ID}, CategoryId: 0},
		{Ids: []uint64{0}, CategoryId: target.ID},
		{CategoryId: target.ID},
		{Ids: make([]uint64, 1001), CategoryId: target.ID},
	} {
		if _, err := s.BatchUpdateProductCategory(ctx, req); err == nil {
			t.Fatalf("invalid request accepted: %v", req)
		}
		if d.Client.Product.GetX(ctx, a.ID).CategoryID != old.ID {
			t.Fatal("partial update escaped rollback")
		}
	}
	req := &adminv1.BatchUpdateProductCategoryRequest{Ids: []uint64{a.ID, b.ID, a.ID}, CategoryId: target.ID}
	for i := 0; i < 2; i++ {
		got, err := s.BatchUpdateProductCategory(ctx, req)
		if err != nil || got.Updated != 2 {
			t.Fatalf("move/retry: %v %v", got, err)
		}
	}
	aa, bb := d.Client.Product.GetX(ctx, a.ID), d.Client.Product.GetX(ctx, b.ID)
	if aa.CategoryID != target.ID || bb.CategoryID != target.ID || aa.Price != 123 || bb.Price != 456 || aa.Status != 1 || bb.Status != 0 {
		t.Fatal("category change modified business fields")
	}
	subctx := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 2})
	if _, err := s.BatchUpdateProductCategory(subctx, &adminv1.BatchUpdateProductCategoryRequest{Ids: []uint64{other.ID}, CategoryId: foreign.ID}); err != nil {
		t.Fatal(err)
	}
	if d.Client.Product.GetX(ctx, a.ID).CategoryID != target.ID {
		t.Fatal("subsite changed main product")
	}
}
