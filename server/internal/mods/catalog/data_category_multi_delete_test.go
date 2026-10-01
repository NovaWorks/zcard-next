package catalog

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"testing"
)

// The multi-select UI uses these existing single-category transactions. Only
// selected empty children may disappear before their selected parent is removed.
func TestCategoryMultiDeletePreservesProductsAndUnselectedChildren(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := context.Background()
	parent := d.Client.Category.Create().SetName("parent").SaveX(ctx)
	child := d.Client.Category.Create().SetName("child").SetParentID(parent.ID).SaveX(ctx)
	keep := d.Client.Category.Create().SetName("keep").SetParentID(parent.ID).SaveX(ctx)
	protected := d.Client.Category.Create().SetName("has-product").SaveX(ctx)
	p := d.Client.Product.Create().SetName("retained product").SetSlug("multi-delete-protected").SetCategoryID(protected.ID).SetPrice(100).SetIsLocked(true).SetStatus(0).SaveX(ctx)
	deleteCategory := func(id uint64) error { _, e := s.DeleteCategory(ctx, &adminv1.DeleteCategoryRequest{Id: id}); return e }
	if deleteCategory(parent.ID) == nil {
		t.Fatal("parent with unselected children deleted")
	}
	if deleteCategory(protected.ID) == nil {
		t.Fatal("category containing a hidden/off-sale product deleted")
	}
	if e := deleteCategory(child.ID); e != nil {
		t.Fatal(e)
	}
	if deleteCategory(parent.ID) == nil {
		t.Fatal("remaining unselected child cascaded")
	}
	if _, e := d.Client.Category.Get(ctx, keep.ID); e != nil {
		t.Fatal("unselected child disappeared", e)
	}
	if got := d.Client.Product.GetX(ctx, p.ID); got.CategoryID != protected.ID || got.Status != 0 || !got.IsLocked {
		t.Fatal("product was modified by failed category deletion")
	}
	// Selecting the complete empty tree permits child-first deletion.
	if e := deleteCategory(keep.ID); e != nil {
		t.Fatal(e)
	}
	if e := deleteCategory(parent.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := d.Client.Category.Get(ctx, parent.ID); !ent.IsNotFound(e) {
		t.Fatal("selected empty parent remains", e)
	}
	if deleteCategory(parent.ID) == nil {
		t.Fatal("duplicate deletion falsely reported success")
	}
	if d.Client.Category.Query().CountX(ctx) != 1 {
		t.Fatal("unexpected category loss")
	}
}
