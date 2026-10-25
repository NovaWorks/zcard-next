package catalog

import (
	"context"
	"errors"
	"fmt"
	"testing"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	orderport "github.com/NovaWorks/zcard-next/server/internal/mods/order/port"
)

func TestStoreProductDetailSalesMatchList(t *testing.T) {
	d, admin := newStatsEnv(t)
	ctx := context.Background()
	store := NewStoreCatalogService(NewCatalogUsecase(NewProductRepoImpl(d, nil)), nil, admin.sold)
	p := d.Client.Product.Create().SetName("sold product").SetSlug("sold-product").SetPrice(100).SetStatus(1).SaveX(ctx)
	other := d.Client.Product.Create().SetName("other product").SetSlug("other-product").SetPrice(100).SetStatus(1).SaveX(ctx)
	unsold := d.Client.Product.Create().SetName("unsold product").SetSlug("unsold-product").SetPrice(100).SetStatus(1).SaveX(ctx)
	for i, sale := range []struct {
		productID uint64
		status    order.Status
		quantity  int32
	}{
		{p.ID, order.StatusPaid, 3},
		{p.ID, order.StatusCompleted, 2},
		{p.ID, order.StatusPendingPayment, 11},
		{p.ID, order.StatusCanceled, 13},
		{p.ID, order.StatusExpired, 17},
		{other.ID, order.StatusPaid, 7},
	} {
		o := d.Client.Order.Create().SetOrderNo(fmt.Sprintf("detail-sales-%d", i)).SetStatus(sale.status).
			SetTotalAmount(0).SetBaseCurrency("CNY").SetVersion(0).SaveX(ctx)
		d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(sale.productID).SetUnitPrice(0).
			SetQuantity(sale.quantity).SetAmount(0).SetFulfillmentType(orderitem.FulfillmentTypeAuto).SaveX(ctx)
	}
	list, err := store.ListProducts(ctx, &storefrontv1.ListProductsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 3 {
		t.Fatalf("listed products=%d, want 3", len(list.Items))
	}
	wants := map[uint64]int64{p.ID: 5, other.ID: 7, unsold.ID: 0}
	for _, listed := range list.Items {
		detail, err := store.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: listed.Id})
		if err != nil {
			t.Fatal(err)
		}
		if listed.SalesCount != wants[listed.Id] || detail.SalesCount != listed.SalesCount {
			t.Errorf("product %d sales: list=%d detail=%d want=%d", listed.Id, listed.SalesCount, detail.SalesCount, wants[listed.Id])
		}
	}
}

type unavailableSoldCounter struct{}

func (unavailableSoldCounter) SoldBatch(context.Context, []uint64) (map[uint64]int64, error) {
	return nil, errors.New("statistics unavailable")
}

func TestStoreProductDetailSalesFallback(t *testing.T) {
	d, _ := newStatsEnv(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("fallback").SetSlug("fallback").SetPrice(100).SetStatus(1).SaveX(ctx)
	for name, counter := range map[string]orderport.SoldCounter{"nil": nil, "error": unavailableSoldCounter{}} {
		t.Run(name, func(t *testing.T) {
			store := NewStoreCatalogService(NewCatalogUsecase(NewProductRepoImpl(d, nil)), nil, counter)
			detail, err := store.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: p.ID})
			if err != nil {
				t.Fatal(err)
			}
			if detail.SalesCount != 0 {
				t.Fatalf("fallback sales=%d, want 0", detail.SalesCount)
			}
		})
	}
}
