package catalog

import (
	"context"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
)

func TestBatchSortOnlyChangesSortAndOrdersBeforePagination(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := context.Background()
	cat := d.Client.Category.Create().SetName("分类").SaveX(ctx)
	a := d.Client.Product.Create().SetName("A").SetSlug("a").SetCategoryID(cat.ID).
		SetPrice(123).SetFactoryPrice(80).SetPointsRequired(9).SetStatus(1).
		SetStockVisible(false).SetIsRecommend(true).SetAutoListing(true).SetSort(10).SaveX(ctx)
	b := d.Client.Product.Create().SetName("B").SetSlug("b").SetCategoryID(cat.ID).SetStatus(1).SetSort(20).SaveX(ctx)
	locked := d.Client.Product.Create().SetName("locked").SetSlug("locked").SetStatus(1).SetSort(30).SetIsLocked(true).SaveX(ctx)
	req := &adminv1.BatchUpdateProductSortRequest{Items: []*adminv1.ProductSortInput{
		{Id: a.ID, Sort: 50, ExpectedSort: 10},
		{Id: b.ID, Sort: 0, ExpectedSort: 20},
		{Id: locked.ID, Sort: 1, ExpectedSort: 30},
	}}
	// Replaying a request after a lost response must preserve the same outcome.
	for i := 0; i < 2; i++ {
		got, err := s.BatchUpdateProductSort(ctx, req)
		if err != nil || got.Updated != 2 || got.SkippedLocked != 1 {
			t.Fatalf("sort/retry: %v %v", got, err)
		}
	}
	aa := d.Client.Product.GetX(ctx, a.ID)
	if aa.Sort != 50 || aa.Name != a.Name || aa.CategoryID != a.CategoryID || aa.Price != a.Price || aa.FactoryPrice != a.FactoryPrice || aa.PointsRequired != a.PointsRequired || aa.Status != a.Status || aa.StockVisible != a.StockVisible || aa.IsRecommend != a.IsRecommend || aa.AutoListing != a.AutoListing {
		t.Fatal("sort update changed other product fields")
	}
	if p := d.Client.Product.GetX(ctx, locked.ID); p.Sort != 30 || !p.IsLocked {
		t.Fatal("locked product was modified")
	}
	for page, want := range []uint64{b.ID, locked.ID, a.ID} {
		rows, _, err := s.repo.ListAdmin(ctx, port.AdminFilter{Page: int32(page + 1), PageSize: 1})
		if err != nil || len(rows) != 1 || rows[0].ID != want {
			t.Fatalf("admin pagination %d: %v %v", page+1, rows, err)
		}
		visible, _, err := s.repo.ListVisible(ctx, port.VisibleFilter{Page: int32(page + 1), PageSize: 1})
		if err != nil || len(visible) != 1 || visible[0].ID != want {
			t.Fatalf("storefront pagination %d: %v %v", page+1, visible, err)
		}
	}
	// A category filter scopes the list; the product's base sort also applies
	// when browsing all categories. Explicit buyer price ordering still wins.
	rows, _, err := s.repo.ListVisible(ctx, port.VisibleFilter{CategoryID: cat.ID, PageSize: 10})
	if err != nil || len(rows) != 2 || rows[0].ID != b.ID || rows[1].ID != a.ID {
		t.Fatalf("category sort: %v %v", rows, err)
	}
	rows, _, err = s.repo.ListVisible(ctx, port.VisibleFilter{Sort: "price_desc", PageSize: 10})
	if err != nil || rows[0].ID != a.ID {
		t.Fatalf("buyer sort: %v %v", rows, err)
	}
}

func TestBatchSortValidationScopeAndAtomicConflicts(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := context.Background()
	a := d.Client.Product.Create().SetName("A").SetSlug("a").SetSort(10).SaveX(ctx)
	b := d.Client.Product.Create().SetName("B").SetSlug("b").SetSort(20).SaveX(ctx)
	foreign := d.Client.Product.Create().SetName("foreign").SetSlug("foreign").SetSubsiteID(2).SaveX(ctx)
	archived := d.Client.Product.Create().SetName("deleted").SetSlug("deleted").SetStatus(deletedProductStatus).SaveX(ctx)
	valid := &adminv1.ProductSortInput{Id: a.ID, Sort: 99, ExpectedSort: 10}
	for _, items := range [][]*adminv1.ProductSortInput{
		nil,
		{nil},
		{{Sort: 1}},
		{{Id: a.ID, Sort: -1, ExpectedSort: 10}},
		{{Id: a.ID, Sort: 1, ExpectedSort: -1}},
		{valid, valid},
		{valid, {Id: 999999, Sort: 1}},
		{valid, {Id: foreign.ID, Sort: 1}},
		{valid, {Id: archived.ID, Sort: 1}},
		make([]*adminv1.ProductSortInput, 1001),
	} {
		if _, err := s.BatchUpdateProductSort(ctx, &adminv1.BatchUpdateProductSortRequest{Items: items}); err == nil {
			t.Fatalf("invalid batch accepted: %v", items)
		}
		if d.Client.Product.GetX(ctx, a.ID).Sort != 10 {
			t.Fatal("invalid batch escaped rollback")
		}
	}
	// The first write must roll back if a later product has changed meanwhile.
	_, err := s.BatchUpdateProductSort(ctx, &adminv1.BatchUpdateProductSortRequest{Items: []*adminv1.ProductSortInput{
		valid, {Id: b.ID, Sort: 1, ExpectedSort: 19},
	}})
	if errors.FromError(err).Reason != "catalog.SORT_STALE" || d.Client.Product.GetX(ctx, a.ID).Sort != 10 || d.Client.Product.GetX(ctx, b.ID).Sort != 20 {
		t.Fatalf("stale batch did not roll back: %v", err)
	}
	subctx := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 2})
	got, err := s.BatchUpdateProductSort(subctx, &adminv1.BatchUpdateProductSortRequest{Items: []*adminv1.ProductSortInput{{Id: foreign.ID, Sort: 42}}})
	if err != nil || got.Updated != 1 || d.Client.Product.GetX(ctx, foreign.ID).Sort != 42 || d.Client.Product.GetX(ctx, a.ID).Sort != 10 {
		t.Fatalf("subsite sort: %v %v", got, err)
	}
}
