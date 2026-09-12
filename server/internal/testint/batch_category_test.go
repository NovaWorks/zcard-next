//go:build integration

package testint

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"testing"
)

func TestBatchCategoryMySQL(t *testing.T) { runBatchCategory(MySQL(t)) }
func TestBatchCategoryPG(t *testing.T)    { runBatchCategory(PG(t)) }
func runBatchCategory(h *Harness) {
	t := h.T
	ctx := context.Background()
	c := h.Data.Client
	svc := catalog.NewAdminCatalogService(catalog.NewProductRepoImpl(h.Data, nil), nil, nil, nil)
	target := c.Category.Create().SetName("目标分类").SaveX(ctx)
	p := c.Product.Create().SetName("商品").SetSlug("batch").SaveX(ctx)
	req := &adminv1.BatchUpdateProductCategoryRequest{Ids: []uint64{p.ID}, CategoryId: target.ID}
	for i := 0; i < 3; i++ {
		got, err := svc.BatchUpdateProductCategory(ctx, req)
		if err != nil || got.Updated != 1 {
			t.Fatalf("move/retry failed: %v %v", got, err)
		}
	}
	other := c.Category.Create().SetName("另一分类").SaveX(ctx)
	req.CategoryId = other.ID
	req.Ids = append(req.Ids, 999999)
	if _, err := svc.BatchUpdateProductCategory(ctx, req); err == nil {
		t.Fatal("stale batch accepted")
	}
	if c.Product.GetX(ctx, p.ID).CategoryID != target.ID {
		t.Fatal("failed batch left partial write")
	}
}
