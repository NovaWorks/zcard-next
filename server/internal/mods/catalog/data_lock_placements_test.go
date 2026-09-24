package catalog

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
	"testing"
	"time"
)

func TestProductLockProtectsMaintenanceAndBatches(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := batchContext()
	cat := d.Client.Category.Create().SetName("分类").SaveX(ctx)
	p := d.Client.Product.Create().SetName("保留").SetSlug("keep").SetPrice(100).SetStatus(0).SetCategoryID(cat.ID).SaveX(ctx)
	other := d.Client.Product.Create().SetName("普通").SetSlug("other").SetStatus(0).SaveX(ctx)
	sku := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("规格").SetSpecValues(map[string]string{}).SaveX(ctx)
	control := d.Client.ProductControl.Create().SetProductID(p.ID).SetName("账号").SetType("text").SaveX(ctx)
	locked, e := s.SetProductLock(ctx, &adminv1.SetProductLockRequest{Id: p.ID, IsLocked: true})
	if e != nil || !locked.IsLocked || locked.LockVersion != 1 {
		t.Fatalf("lock: %v %v", locked, e)
	}
	checks := map[string]func() error{
		"product": func() error {
			_, e := s.UpdateProduct(ctx, &adminv1.UpdateProductRequest{Id: p.ID, Name: "覆盖"})
			return e
		},
		"delete": func() error {
			_, e := s.DeleteProduct(ctx, &adminv1.DeleteProductRequest{Id: p.ID, DeleteOrders: true, ConfirmName: p.Name})
			return e
		},
		"sku update": func() error { _, e := s.repo.UpdateSku(ctx, sku.ID, SkuInput{Name: "覆盖"}); return e },
		"sku create": func() error {
			_, e := s.repo.CreateSku(ctx, SkuInput{ProductID: p.ID, Name: "new", SpecValues: map[string]string{}})
			return e
		},
		"sku delete": func() error { _, e := s.DeleteSku(ctx, &adminv1.DeleteSkuRequest{Id: sku.ID}); return e },
		"control update": func() error {
			_, e := s.repo.UpdateProductControl(ctx, control.ID, "覆盖", "text", false, nil, 0)
			return e
		},
		"control delete": func() error { _, e := s.DeleteControl(ctx, &adminv1.DeleteControlRequest{Id: control.ID}); return e },
	}
	for name, fn := range checks {
		t.Run(name, func(t *testing.T) {
			if !data.IsProductLocked(fn()) {
				t.Fatal("locked mutation was not rejected")
			}
		})
	}
	preview, e := s.PreviewDeleteProduct(ctx, &adminv1.GetProductRequest{Id: p.ID})
	if e != nil || preview.DeleteBlockReason == "" {
		t.Fatal("missing lock explanation")
	}
	status, e := s.BatchUpdateProductStatus(ctx, &adminv1.BatchUpdateProductStatusRequest{Ids: []uint64{p.ID, other.ID, p.ID}, Status: 1})
	if e != nil || status.Updated != 1 || status.SkippedLocked != 1 {
		t.Fatalf("batch: %v %v", status, e)
	}
	moved, e := s.BatchUpdateProductCategory(ctx, &adminv1.BatchUpdateProductCategoryRequest{Ids: []uint64{p.ID, other.ID}, CategoryId: cat.ID})
	if e != nil || moved.Updated != 1 || moved.SkippedLocked != 1 {
		t.Fatalf("category batch: %v %v", moved, e)
	}
	got := d.Client.Product.GetX(ctx, p.ID)
	if got.Name != p.Name || got.Price != p.Price || got.Status != 0 || !got.IsLocked {
		t.Fatal("locked product changed")
	}
	if _, e = s.SetProductLock(ctx, &adminv1.SetProductLockRequest{Id: p.ID, IsLocked: false}); errors.FromError(e).Reason != "catalog.LOCK_STALE" {
		t.Fatal("stale unlock allowed")
	}
	foreign := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 7})
	if _, e = s.SetProductLock(foreign, &adminv1.SetProductLockRequest{Id: p.ID, ExpectedVersion: 1}); e == nil {
		t.Fatal("cross tenant unlock")
	}
	if _, e = s.SetProductLock(ctx, &adminv1.SetProductLockRequest{Id: p.ID, ExpectedVersion: 1}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.repo.UpdateSku(ctx, sku.ID, SkuInput{Name: "解锁后"}); e != nil {
		t.Fatal(e)
	}
}

func TestContentBatchSkipsLocksAtPreviewAndCommit(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := batchContext()
	ids := []uint64{}
	for _, slug := range []string{"a", "b", "c"} {
		ids = append(ids, d.Client.Product.Create().SetName(slug).SetSlug(slug).SetDescription("保留").SaveX(ctx).ID)
	}
	if _, e := s.SetProductLock(ctx, &adminv1.SetProductLockRequest{Id: ids[0], IsLocked: true}); e != nil {
		t.Fatal(e)
	}
	v := previewContent(t, s, ctx, ids, &adminv1.ProductContentPatch{DescriptionAction: 2})
	if v.SkippedLocked != 1 || v.Changed != 2 || len(v.Targets) != 2 {
		t.Fatalf("preview %v", v)
	}
	if _, e := s.SetProductLock(ctx, &adminv1.SetProductLockRequest{Id: ids[1], IsLocked: true}); e != nil {
		t.Fatal(e)
	}
	result := applyContent(t, s, ctx, v)
	if result.Changed != 1 || result.SkippedLocked != 2 || result.Unchanged != 0 {
		t.Fatalf("receipt %v", result)
	}
	again := applyContent(t, s, ctx, v)
	if again.Changed != 1 || again.SkippedLocked != 2 {
		t.Fatal("retry lost skip result")
	}
	for _, id := range ids[:2] {
		if d.Client.Product.GetX(ctx, id).Description != "保留" {
			t.Fatal("locked content changed")
		}
	}
}

func TestCategoryPlacementScopeOrderVisibilityAndConflict(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := batchContext()
	root := d.Client.Category.Create().SetName("父分类").SaveX(ctx)
	child := d.Client.Category.Create().SetName("子分类").SetParentID(root.ID).SaveX(ctx)
	outside := d.Client.Category.Create().SetName("其他分类").SaveX(ctx)
	a := d.Client.Product.Create().SetName("A").SetSlug("a").SetPrice(300).SetCategoryID(child.ID).SaveX(ctx)
	b := d.Client.Product.Create().SetName("B").SetSlug("b").SetPrice(100).SetCategoryID(root.ID).SaveX(ctx)
	c := d.Client.Product.Create().SetName("C").SetSlug("c").SetPrice(200).SetCategoryID(root.ID).SaveX(ctx)
	input := &adminv1.SetCategoryPlacementsRequest{CategoryId: root.ID, Items: []*adminv1.CategoryPlacementInput{{ProductId: a.ID, IsPinned: true, IsRecommended: true}, {ProductId: b.ID, IsRecommended: true}}}
	out, e := s.SetCategoryPlacements(ctx, input)
	if e != nil {
		t.Fatal(e)
	}
	if out.Version != 1 {
		t.Fatal("version")
	}
	if _, e = s.SetCategoryPlacements(ctx, input); errors.FromError(e).Reason != "catalog.PLACEMENT_STALE" {
		t.Fatal("lost-update accepted")
	}
	list := func(filter port.VisibleFilter) []port.Product {
		t.Helper()
		rows, _, e := s.repo.ListVisible(ctx, filter)
		if e != nil {
			t.Fatal(e)
		}
		return rows
	}
	first := list(port.VisibleFilter{CategoryID: root.ID, Page: 1, PageSize: 2})
	second := list(port.VisibleFilter{CategoryID: root.ID, Page: 2, PageSize: 2})
	if len(first) != 2 || first[0].ID != a.ID || first[1].ID != c.ID || len(second) != 1 || second[0].ID != b.ID || !first[0].CategoryRecommend {
		t.Fatalf("pagination %v %v", first, second)
	}
	prices := list(port.VisibleFilter{CategoryID: root.ID, Sort: "price_asc"})
	if prices[0].ID != b.ID || prices[2].ID != a.ID || !prices[2].CategoryRecommend {
		t.Fatal("explicit price sort overridden")
	}
	sub := list(port.VisibleFilter{CategoryID: child.ID})
	if len(sub) != 1 || sub[0].CategoryPinned || sub[0].CategoryRecommend {
		t.Fatal("parent placement leaked into child")
	}
	all := list(port.VisibleFilter{})
	for _, p := range all {
		if p.CategoryRecommend || p.CategoryPinned {
			t.Fatal("category state leaked globally")
		}
	}
	home := list(port.VisibleFilter{RecommendOnly: true})
	if len(home) != 0 {
		t.Fatal("category recommendation leaked into homepage")
	}
	if _, e = s.SetProductLock(ctx, &adminv1.SetProductLockRequest{Id: a.ID, IsLocked: true}); e != nil {
		t.Fatal(e)
	}
	if e = s.repo.ReorderCategories(ctx, outside.ID, []uint64{child.ID}); !data.IsProductLocked(e) {
		t.Fatal("category drag moved locked descendant")
	}
	if e = s.repo.ReorderCategories(ctx, root.ID, []uint64{child.ID}); e != nil {
		t.Fatal("same-parent reorder blocked", e)
	}
	// Unchanged locked entry is allowed; altering/removing it is not.
	input.ExpectedVersion = 1
	if _, e = s.SetCategoryPlacements(ctx, input); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetCategoryPlacements(ctx, &adminv1.SetCategoryPlacementsRequest{CategoryId: root.ID, ExpectedVersion: 2}); !data.IsProductLocked(e) {
		t.Fatal("locked placement removed")
	}
	if _, e = s.MergeCategories(ctx, &adminv1.MergeCategoriesRequest{SourceIds: []uint64{root.ID}, TargetId: outside.ID}); !data.IsProductLocked(e) {
		t.Fatal("merge moved locked descendant")
	}
	if _, e = s.SetCategoryPlacements(ctx, &adminv1.SetCategoryPlacementsRequest{CategoryId: outside.ID, Items: input.Items}); e == nil {
		t.Fatal("out of scope product allowed")
	}
	// Visibility always wins over pinning.
	d.Client.Category.UpdateOneID(child.ID).SetHide(true).ExecX(ctx)
	visible := list(port.VisibleFilter{CategoryID: root.ID})
	for _, p := range visible {
		if p.ID == a.ID {
			t.Fatal("pin bypassed hidden category")
		}
	}
	// Moving an unlocked product out of scope leaves a diagnosable, inactive setting.
	d.Client.Product.UpdateOneID(b.ID).SetCategoryID(outside.ID).ExecX(ctx)
	snapshot, e := s.GetCategoryPlacements(ctx, &adminv1.GetCategoryPlacementsRequest{CategoryId: root.ID})
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range snapshot.Items {
		if p.UnavailableReason == "" {
			t.Fatal("missing non-display reason")
		}
	}
}

func TestProductLockSerializesWithInFlightMaintenance(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := batchContext()
	d.DB.SetMaxOpenConns(2)
	p := d.Client.Product.Create().SetName("before").SetSlug("concurrent").SaveX(ctx)
	guarded := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- data.Tx(ctx, d, func(tx context.Context) error {
			if _, e := data.GuardProductWrite(tx, d, p.ID); e != nil {
				return e
			}
			close(guarded)
			<-release
			return data.Client(tx, d).Product.UpdateOneID(p.ID).SetName("committed before lock").Exec(tx)
		})
	}()
	<-guarded
	locking := make(chan error, 1)
	go func() {
		_, e := s.SetProductLock(ctx, &adminv1.SetProductLockRequest{Id: p.ID, IsLocked: true})
		locking <- e
	}()
	select {
	case e := <-locking:
		close(release)
		<-done
		t.Fatalf("lock completed before guarded write: %v", e)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if e := <-locking; e != nil {
		t.Fatal(e)
	}
	if _, e := s.repo.UpdateProduct(ctx, p.ID, port.ProductInput{Name: "late"}); !data.IsProductLocked(e) {
		t.Fatal("late edit bypassed lock")
	}
	if got := d.Client.Product.GetX(ctx, p.ID); got.Name != "committed before lock" || !got.IsLocked {
		t.Fatal(got)
	}
}
