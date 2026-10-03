package fulfillment

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"testing"
)

func TestMixedOrderFetchingCardsDoesNotConfirmParcel(t *testing.T) {
	d, cipher, repo := newFulfillData(t)
	ctx := context.Background()
	product, o := seedPaidOrderWithCards(t, d, cipher, "status", 1)
	d.Client.Order.UpdateOneID(o.ID).SetCommerceVersion(1).ExecX(ctx)
	physical := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(product).SetQuantity(1).SetUnitPrice(1000).SetAmount(1000).SetGoodsType("physical").SetFulfillmentType(orderitem.FulfillmentTypeShipping).SetShippedQuantity(1).SaveX(ctx)
	if err := repo.FulfillOrder(ctx, o.OrderNo); err != nil {
		t.Fatal(err)
	}
	owner := identity.WithClaims(ctx, &authn.Claims{Subject: o.UserID})
	result, err := repo.FetchDelivery(owner, o.OrderNo, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Status != "delivered" || string(d.Client.Order.GetX(ctx, o.ID).Status) != "delivered" {
		t.Fatalf("fetch confirmed unreceived parcel: %+v", result)
	}
	d.Client.OrderItem.UpdateOneID(physical.ID).SetReceivedQuantity(1).ExecX(ctx)
	if err := data.RefreshPhysicalProgress(ctx, d, d.Client.Order.GetX(ctx, o.ID)); err != nil {
		t.Fatal(err)
	}
	if string(d.Client.Order.GetX(ctx, o.ID).Status) != "completed" {
		t.Fatal("received mixed order did not complete")
	}
}
