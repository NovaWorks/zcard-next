package fulfillment

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"testing"
)

func TestDeletedCategoryOrdersStillFulfillAndFetch(t *testing.T) {
	for _, mode := range []string{"status", "delete"} {
		t.Run(mode, func(t *testing.T) {
			d, cipher, delivery := newFulfillData(t)
			ctx := context.Background()
			pid, o := seedPaidOrderWithCards(t, d, cipher, mode, 2)
			cat := d.Client.Category.Create().SetName("removed category").SaveX(ctx)
			d.Client.Product.UpdateOneID(pid).SetCategoryID(cat.ID).SetIsLocked(true).ExecX(ctx)
			catalogService := catalog.NewAdminCatalogService(catalog.NewProductRepoImpl(d, nil), nil, nil, cipher)
			if _, err := catalogService.DeleteCategory(ctx, &adminv1.DeleteCategoryRequest{Id: cat.ID}); err != nil {
				t.Fatal(err)
			}
			if err := delivery.FulfillOrder(ctx, o.OrderNo); err != nil {
				t.Fatal("fulfillment after deletion", err)
			}
			result, err := delivery.FetchDelivery(ctx, o.OrderNo, "", "127.0.0.1")
			if err != nil || len(result.Items) != 2 || (mode == "status" && result.Items[0].Content != "CARD-SECRET-0") || (mode == "delete" && !result.Items[0].Masked) {
				t.Fatalf("historical pickup: %+v %v", result, err)
			}
			if d.Client.Product.GetX(ctx, pid).Status != -1 {
				t.Fatal("fulfillment revived product")
			}
		})
	}
}
