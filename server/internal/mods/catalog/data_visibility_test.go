package catalog

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"testing"
)

func TestCategoryVisibilityCascadesWithoutChangingProducts(t *testing.T) {
	d, _ := newStatsEnv(t)
	ctx := context.Background()
	c := d.Client
	parent := c.Category.Create().SetName("父级").SaveX(ctx)
	child := c.Category.Create().SetName("子级").SetParentID(parent.ID).SaveX(ctx)
	grandchild := c.Category.Create().SetName("孙级").SetParentID(child.ID).SaveX(ctx)
	p := c.Product.Create().SetName("隐藏商品").SetSlug("hidden").SetPrice(100).SetStatus(1).SetIsRecommend(true).SetCategoryID(grandchild.ID).SaveX(ctx)
	visible := c.Product.Create().SetName("未分类商品").SetSlug("visible").SetPrice(100).SetStatus(1).SaveX(ctx)
	repo := NewProductRepoImpl(d, nil)
	hidden := true
	if _, err := repo.UpdateCategory(ctx, parent.ID, "", nil, &hidden, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, f := range []port.VisibleFilter{{Page: 1, PageSize: 10}, {CategoryID: parent.ID}, {Keyword: "隐藏"}, {RecommendOnly: true}} {
		items, total, err := repo.ListVisible(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID == p.ID {
				t.Fatal("hidden product exposed")
			}
		}
		if f.Keyword != "" && total != 0 {
			t.Fatal(total)
		}
	}
	categories, err := repo.ListVisibleCategories(ctx, 0)
	if err != nil || len(categories) != 0 {
		t.Fatal(categories, err)
	}
	got, err := repo.Get(ctx, 0, p.ID)
	if err != nil || got.Status == 1 {
		t.Fatal(got, err)
	}
	if _, err := repo.GetForSupply(ctx, p.ID); err == nil {
		t.Fatal("supply detail exposed")
	}
	items, total, err := repo.ListForSupply(ctx, port.AdminFilter{Status: -1, Page: 1, PageSize: 20})
	if err != nil || total != 1 || items[0].ID != visible.ID {
		t.Fatal(items, total, err)
	}
	if c.Product.GetX(ctx, p.ID).Status != 1 {
		t.Fatal("visibility overwrote product status")
	}
	hidden = false
	if _, err := repo.UpdateCategory(ctx, parent.ID, "", nil, &hidden, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(ctx, 0, p.ID)
	if err != nil || got.Status != 1 {
		t.Fatal(got, err)
	}
	c.Category.UpdateOneID(child.ID).SetHide(true).ExecX(ctx)
	got, err = repo.Get(ctx, 0, p.ID)
	if err != nil || got.Status == 1 {
		t.Fatal("child own hide lost", got, err)
	}
	if _, err = repo.Get(ctx, 0, visible.ID); err != nil {
		t.Fatal(err)
	}
	foreign := c.Category.Create().SetName("外站").SetSubsiteID(7).SaveX(ctx)
	if _, err = repo.UpdateCategory(ctx, foreign.ID, "", nil, &hidden, nil, nil); err == nil {
		t.Fatal("cross tenant write allowed")
	}
}
