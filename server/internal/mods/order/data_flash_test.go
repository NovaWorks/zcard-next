package order

import (
	"context"
	"errors"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	catalogmod "github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	couponmod "github.com/NovaWorks/zcard-next/server/internal/mods/coupon"
	couponport "github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"
	"testing"
	"time"
)

type failingFlash struct{ couponport.FlashResolver }

func (failingFlash) Active(context.Context, uint64, uint64) (*couponport.FlashInfo, error) {
	return nil, errors.New("flash unavailable")
}
func TestFlashCheckoutSkuLimitsAndReadFailure(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	d.Client.Product.UpdateOneID(1).SetPrice(3000).SaveX(ctx)
	sku := d.Client.ProductSku.Create().SetProductID(1).SetName("variant").SetPrice(4000).SetSpecValues(map[string]string{}).SaveX(ctx)
	uc.Catalog = catalogmod.NewProductRepoImpl(d, nil)
	flash := couponmod.NewCouponRepoImpl(d)
	uc.Flash = flash
	now := time.Now().UTC()
	offer, err := flash.CreateFlash(ctx, 1, 0, 2000, now.Add(-time.Hour), now.Add(time.Hour), 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	create := func(skuID uint64) (*CreateOrderResult, error) {
		return uc.CreateOrder(ctx, CreateOrderInput{UserID: 7, QueryPassword: "abcd", Items: []OrderItemInput{{ProductID: 1, SkuID: skuID, Quantity: 1}}})
	}
	for _, id := range []uint64{sku.ID, sku.ID} {
		o, err := create(id)
		if err != nil || o.TotalCents != 2000 {
			t.Fatalf("checkout SKU %d: %+v %v", id, o, err)
		}
	}
	if _, err := create(sku.ID); !errors.Is(err, couponport.ErrFlashUserLimit) {
		t.Fatalf("limit bypassed: %v", err)
	}
	if got := d.Client.FlashSale.GetX(ctx, offer.ID); got.SoldQty != 0 || got.ReservedQty != 2 {
		t.Fatalf("unpaid quota: sold=%d reserved=%d", got.SoldQty, got.ReservedQty)
	}
	uc.Flash = failingFlash{}
	if _, err := create(sku.ID); err == nil {
		t.Fatal("read failure silently charged regular price")
	}
	if d.Client.Order.Query().CountX(ctx) != 2 {
		t.Fatal("failed order was committed")
	}
	uc.Flash = flash
	d.Client.FlashSale.UpdateOneID(offer.ID).SetEndAt(now.Add(-time.Second)).SaveX(ctx)
	o, err := create(sku.ID)
	if err != nil || o.TotalCents != 4000 {
		t.Fatalf("expired price: %+v %v", o, err)
	}
}

func TestCartExposesCurrentFlashOffer(t *testing.T) {
	svc, d := newCartEnv(t)
	ctx := userCtx(7)
	p := d.Client.Product.Create().SetName("flash cart").SetSlug("flash-cart").SetPrice(3000).SetStockType("card").SetStatus(1).SaveX(ctx)
	repo := couponmod.NewCouponRepoImpl(d)
	svc.flash = repo
	now := time.Now().UTC()
	offer, err := repo.CreateFlash(ctx, p.ID, 0, 2000, now.Add(-time.Hour), now.Add(time.Hour), 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	it, err := svc.AddCartItem(ctx, &storefrontv1.AddCartItemRequest{ProductId: p.ID, Quantity: 1})
	if err != nil || it.PriceCents != 3000 || it.GetFlashSale().GetPriceCents() != 2000 {
		t.Fatalf("cart offer: %+v %v", it, err)
	}
	d.Client.FlashSale.UpdateOneID(offer.ID).SetEndAt(now.Add(-time.Second)).SaveX(ctx)
	list, err := svc.ListCart(ctx, nil)
	if err != nil || len(list.Items) != 1 || list.Items[0].FlashSale != nil {
		t.Fatalf("expired cart: %+v %v", list, err)
	}
	svc.flash = failingFlash{}
	if _, err := svc.ListCart(ctx, nil); err == nil {
		t.Fatal("cart read error hidden")
	}
}
