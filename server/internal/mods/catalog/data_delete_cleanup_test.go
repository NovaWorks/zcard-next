package catalog

import (
	"context"
	"errors"
	"fmt"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	kerrors "github.com/go-kratos/kratos/v3/errors"
)

func TestCategoryDeleteReleasesLegacyArchivesAndKeepsHistory(t *testing.T) {
	d, s, p, o, _ := deleteFixture(t, order.StatusCompleted)
	ctx := context.Background()
	cat := d.Client.Category.Create().SetName("empty").SaveX(ctx)
	d.Client.Product.UpdateOneID(p.ID).SetCategoryID(cat.ID).SetStatus(-1).ExecX(ctx)
	d.Client.CategoryProductPlacement.Create().SetCategoryID(cat.ID).SetProductID(p.ID).SaveX(ctx)
	d.Client.SupplierProductPrice.Create().SetSupplierAccountID(1).SetProductID(0).SetCategoryID(cat.ID).SetScope("category").SetPrice(0).SetDiscountBps(9000).ExecX(ctx)
	mapping := d.Client.SupplyMapping.Create().SetConnectionID(1).SetUpstreamProduct("legacy").SetLocalCategoryID(cat.ID).SaveX(ctx)
	counts, err := s.repo.CategoryProductCounts(ctx, []*ent.Category{cat})
	if err != nil || counts[cat.ID] != 0 {
		t.Fatalf("empty category count %v %v", counts, err)
	}
	if _, err = s.DeleteCategory(ctx, &adminv1.DeleteCategoryRequest{Id: cat.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Client.Category.Get(ctx, cat.ID); !ent.IsNotFound(err) {
		t.Fatal("category remains", err)
	}
	if got := d.Client.Product.GetX(ctx, p.ID); got.CategoryID != 0 || string(got.DirectContent) != "encrypted-content" {
		t.Fatal("archived category not cleared or history damaged")
	}
	if !d.Client.Order.Query().ExistX(ctx) || d.Client.Order.GetX(ctx, o.ID).Status != order.StatusCompleted {
		t.Fatal("historical order lost")
	}
	if d.Client.CategoryProductPlacement.Query().ExistX(ctx) || d.Client.SupplierProductPrice.Query().ExistX(ctx) {
		t.Fatal("stale category configuration remains")
	}
	if d.Client.SupplyMapping.GetX(ctx, mapping.ID).LocalCategoryID != 0 {
		t.Fatal("upstream mapping still refers to deleted category")
	}
}

func TestCategoryDeletionReasonsAndTenantBoundary(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := context.Background()
	parent := d.Client.Category.Create().SetName("parent").SaveX(ctx)
	d.Client.Category.Create().SetName("child").SetParentID(parent.ID).SaveX(ctx)
	foreign := d.Client.Category.Create().SetName("foreign").SetSubsiteID(9).SaveX(ctx)
	check := func(ctx context.Context, id uint64, code int, reason string) {
		t.Helper()
		_, err := s.DeleteCategory(ctx, &adminv1.DeleteCategoryRequest{Id: id})
		e := kerrors.FromError(err)
		if err == nil || int(e.Code) != code || e.Reason != reason {
			t.Fatalf("delete %d: %v, want %d/%s", id, err, code, reason)
		}
	}

	check(ctx, foreign.ID, 404, "catalog.CATEGORY_NOT_FOUND")
	other := tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 9})
	check(other, parent.ID, 404, "catalog.CATEGORY_NOT_FOUND")
	for _, status := range []int8{0, 1, 2} {
		cat := d.Client.Category.Create().SetName(fmt.Sprintf("status%d", status)).SaveX(ctx)
		d.Client.Product.Create().SetName("kept").SetSlug(fmt.Sprintf("kept%d", status)).SetPrice(1).SetStatus(status).SetCategoryID(cat.ID).SaveX(ctx)
		if _, e := s.DeleteCategory(ctx, &adminv1.DeleteCategoryRequest{Id: cat.ID}); e != nil {
			t.Fatal(e)
		}
	}
	if d.Client.Category.Query().CountX(ctx) != 3 {
		t.Fatal("failed deletion modified categories")
	}
}

func TestProductDeletionRemovesEveryCatalogAndReleasesCategory(t *testing.T) {
	for _, status := range []int8{0, 1, 2} {
		t.Run(fmt.Sprintf("status%d", status), func(t *testing.T) {
			d, s, p, o, _ := deleteFixture(t, order.StatusCompleted)
			ctx := context.Background()
			cat := d.Client.Category.Create().SetName("empty-after-delete").SaveX(ctx)
			d.Client.Product.UpdateOneID(p.ID).SetCategoryID(cat.ID).SetStatus(status).SetIsRecommend(true).SetAutoListing(true).ExecX(ctx)
			d.Client.CategoryProductPlacement.Create().SetCategoryID(cat.ID).SetProductID(p.ID).SetIsPinned(true).ExecX(ctx)
			d.Client.SupplierProductPrice.Create().SetSupplierAccountID(1).SetProductID(p.ID).SetPrice(80).ExecX(ctx)
			d.Client.SupplyMapping.Create().SetConnectionID(1).SetUpstreamProduct("old").SetLocalProductID(p.ID).ExecX(ctx)
			d.Client.CartItem.Create().SetUserID(1).SetProductID(p.ID).SetQuantity(1).ExecX(ctx)
			unsold := d.Client.Card.Create().SetProductID(p.ID).SetContent([]byte("unsold-secret")).SetContentHash("unsold").SetStatus(card.StatusAvailable).SaveX(ctx)
			pre, err := s.PreviewDeleteProduct(ctx, &adminv1.GetProductRequest{Id: p.ID})
			if err != nil || pre.DeleteBlockReason != "" {
				t.Fatalf("deletable product blocked: %v %v", pre, err)
			}
			if _, err = s.DeleteProduct(ctx, &adminv1.DeleteProductRequest{Id: p.ID}); err != nil {
				t.Fatal(err)
			}
			got := d.Client.Product.GetX(ctx, p.ID)
			if got.Status != -1 || got.CategoryID != 0 || got.IsRecommend || got.AutoListing {
				t.Fatalf("incomplete deletion: %+v", got)
			}
			if d.Client.CategoryProductPlacement.Query().ExistX(ctx) || d.Client.SupplierProductPrice.Query().ExistX(ctx) || d.Client.SupplyMapping.Query().ExistX(ctx) || d.Client.CartItem.Query().ExistX(ctx) {
				t.Fatal("live catalog configuration left behind")
			}
			if _, err = s.repo.GetForSupply(ctx, p.ID); !errors.Is(err, ErrProductNotFound) {
				t.Fatal("supplier detail exposes archive", err)
			}
			if _, err = NewCatalogUsecase(s.repo).GetVisible(ctx, 0, p.ID); !errors.Is(err, ErrProductNotFound) {
				t.Fatal("storefront detail exposes archive", err)
			}
			rows, _, err := s.repo.ListForSupply(ctx, port.AdminFilter{Status: -1, Page: 1, PageSize: 20})
			if err != nil || len(rows) != 0 {
				t.Fatal("supplier list exposes archive", err)
			}
			visible, _, err := s.repo.ListVisible(ctx, port.VisibleFilter{Page: 1, PageSize: 20})
			if err != nil || len(visible) != 0 {
				t.Fatal("storefront list exposes archive", err)
			}
			if d.Client.Order.GetX(ctx, o.ID).Status != order.StatusCompleted || string(d.Client.Card.GetX(ctx, unsold.ID).Content) != "unsold-secret" || d.Client.Card.GetX(ctx, unsold.ID).Status != card.StatusDisabled {
				t.Fatal("archive data damaged")
			}
			if _, err = s.DeleteCategory(ctx, &adminv1.DeleteCategoryRequest{Id: cat.ID}); err != nil {
				t.Fatal("category still blocked after product deletion", err)
			}
		})
	}
}

func TestPhysicalProductArchiveKeepsStockCompensation(t *testing.T) {
	d, s, p, _, _ := deleteFixture(t, order.StatusCompleted)
	ctx := context.Background()
	d.Client.Product.UpdateOneID(p.ID).SetGoodsType("physical").SetPhysicalStock(5).ExecX(ctx)
	pre, err := s.PreviewDeleteProduct(ctx, &adminv1.GetProductRequest{Id: p.ID})
	if err != nil || pre.DeleteBlockReason != "" || pre.DeleteOrdersBlockReason == "" {
		t.Fatalf("physical archive preview: %v %v", pre, err)
	}
	if _, err = s.DeleteProduct(ctx, &adminv1.DeleteProductRequest{Id: p.ID}); err != nil {
		t.Fatal(err)
	}
	err = data.Tx(ctx, d, func(ctx context.Context) error {
		return data.MovePhysicalStock(ctx, d, 0, p.ID, 0, 0, 1, "return:archived", "历史订单退货入库")
	})
	if err != nil || d.Client.Product.GetX(ctx, p.ID).PhysicalStock != 6 || d.Client.Product.GetX(ctx, p.ID).Status != -1 {
		t.Fatal("historical stock compensation failed or revived product", err)
	}
}
