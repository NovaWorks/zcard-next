package order

import (
	"context"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestAdminLevelChangeUpdatesCatalogCartAndCheckout(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	d.Client.Product.UpdateOneID(1).SetPrice(10001).SetStockType("url").ExecX(ctx)
	sku := d.Client.ProductSku.Create().SetProductID(1).SetName("variant").SetPrice(20001).SetSpecValues(map[string]string{}).SaveX(ctx)
	inherited := d.Client.ProductSku.Create().SetProductID(1).SetName("inherited").SetSpecValues(map[string]string{}).SaveX(ctx)
	buyer := d.Client.User.Create().SetUsername("member-price-buyer").SaveX(ctx)
	level := d.Client.MemberLevel.Create().SetName("代理").SetAcquireMode("manual").SetDisplayMode("hidden").SetDiscount(8500).SaveX(ctx)
	levels := memberlevel.NewMemberLevelRepoImpl(d, nil)
	repo := catalog.NewProductRepoImpl(d, nil)
	uc.MemberRate, uc.Catalog = levels, repo
	store := catalog.NewStoreCatalogService(catalog.NewCatalogUsecase(repo), nil, nil)
	store.SetDiscountResolver(uc)
	cart := NewStoreCartService(d, repo, nil, settings.NewRepoImpl(d), nil, uc)
	actor := identity.WithClaims(ctx, &authn.Claims{Subject: 99, Realm: authn.RealmAdmin})
	// Keep the same login claims across assignments; the token contains no price.
	loggedIn := identity.WithClaims(ctx, &authn.Claims{Subject: buyer.ID, Realm: authn.RealmUser})
	d.Client.CartItem.Create().SetUserID(buyer.ID).SetProductID(1).SetSkuID(sku.ID).SetQuantity(2).ExecX(ctx)
	admin := memberlevel.NewAdminMemberLevelService(levels)
	group := d.Client.MemberProductGroup.Create().SetName("group").SetProductIds([]uint64{1}).SetDiscount(10000).SaveX(ctx)
	for _, tc := range []struct {
		name      string
		levelID   uint64
		base, sku int64
		groupRate int32
		stack     bool
	}{
		{"before assignment", 0, 10001, 20001, 10000, false},
		{"assigned hidden level", level.ID, 8501, 17001, 10000, false},
		{"member discount is better", level.ID, 8501, 17001, 9500, false},
		{"group discount is better", level.ID, 8001, 16001, 8000, false},
		{"stacked discounts", level.ID, 8076, 16151, 9500, true},
		{"removed assignment", 0, 10001, 20001, 10000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d.Client.MemberProductGroup.UpdateOneID(group.ID).SetDiscount(tc.groupRate).SetStackMember(tc.stack).ExecX(ctx)
			if _, err := admin.AssignUserLevel(actor, &adminv1.AssignUserLevelRequest{UserId: buyer.ID, LevelId: tc.levelID, Reason: "price regression"}); err != nil {
				t.Fatal(err)
			}
			list, err := store.ListProducts(loggedIn, &storefrontv1.ListProductsRequest{})
			if err != nil || len(list.GetItems()) != 1 || list.Items[0].PriceCents != tc.base {
				t.Fatalf("list: %v %v", list, err)
			}
			detail, err := store.GetProduct(loggedIn, &storefrontv1.GetProductRequest{Id: 1})
			if err != nil || detail.GetPriceCents() != tc.base {
				t.Fatalf("detail: %v %v", detail, err)
			}
			prices := map[uint64]int64{}
			for _, v := range detail.Skus {
				prices[v.Id] = v.PriceCents
			}
			if prices[sku.ID] != tc.sku || prices[inherited.ID] != tc.base {
				t.Fatal("SKU prices", prices)
			}
			items, err := cart.ListCart(loggedIn, &emptypb.Empty{})
			if err != nil || len(items.GetItems()) != 1 || items.Items[0].PriceCents != tc.sku {
				t.Fatalf("cart: %v %v", items, err)
			}
			quote, err := NewStoreOrderService(uc, nil).QuoteOrder(loggedIn, &storefrontv1.CreateOrderRequest{QueryPassword: "price-test", Items: []*storefrontv1.OrderItemInput{{ProductId: 1, SkuId: sku.ID, Quantity: 2}}})
			if err != nil || quote.GetTotalCents() != tc.sku*2 {
				t.Fatalf("quote: %v %v", quote, err)
			}
			guest, err := store.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: 1})
			guestPrice := int64(10001)
			if tc.groupRate < 10000 {
				guestPrice -= guestPrice * int64(10000-tc.groupRate) / 10000
			}
			if err != nil || guest.GetPriceCents() != guestPrice {
				t.Fatalf("member price leaked to guest: %v %v", guest, err)
			}
		})
	}
	if _, err := admin.AssignUserLevel(actor, &adminv1.AssignUserLevelRequest{UserId: buyer.ID, LevelId: level.ID, Reason: "disabled level regression"}); err != nil {
		t.Fatal(err)
	}
	d.Client.MemberLevel.UpdateOneID(level.ID).SetEnabled(false).ExecX(ctx)
	if _, err := store.GetProduct(loggedIn, &storefrontv1.GetProductRequest{Id: 1}); err == nil {
		t.Fatal("invalid assigned level silently advertised full price")
	}
	if _, err := cart.ListCart(loggedIn, &emptypb.Empty{}); err == nil {
		t.Fatal("invalid assigned level silently advertised full cart price")
	}
}
