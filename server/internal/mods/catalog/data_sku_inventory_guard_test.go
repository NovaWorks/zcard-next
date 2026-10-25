package catalog

import (
	"context"
	"fmt"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
)

func TestDeleteSkuKeepsInventoryOrdersAndSources(t *testing.T) {
	for _, state := range []string{"available", "reserved", "disabled", "order", "source", "unconfirmed-source"} {
		t.Run(state, func(t *testing.T) {
			d, _ := newStatsEnv(t)
			ctx := context.Background()
			p := d.Client.Product.Create().SetName("guarded").SetSlug("guarded").SetPrice(100).SetStatus(1).SaveX(ctx)
			sk := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("monthly").SetSpecValues(map[string]string{}).SaveX(ctx)
			switch state {
			case "order":
				o := d.Client.Order.Create().SetOrderNo("old-order").SaveX(ctx)
				d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetSkuID(sk.ID).SetQuantity(1).SetUnitPrice(100).SetAmount(100).SetFulfillmentType("auto").SaveX(ctx)
			case "source", "unconfirmed-source":
				b := d.Client.ProductDeliverySource.Create().SetProductID(p.ID).SetSkuID(sk.ID)
				if state == "source" {
					b.SetCurrentKey(data.DeliverySourceKey(0, p.ID, sk.ID)).SetStatus("paused")
				} else {
					b.SetStatus("uncertain")
				}
				b.SaveX(ctx)
			default:
				d.Client.Card.Create().SetProductID(p.ID).SetSkuID(sk.ID).SetContent([]byte("encrypted")).SetContentHash(fmt.Sprint(sk.ID)).SetStatus(card.Status(state)).SaveX(ctx)
			}
			if err := NewProductRepoImpl(d, nil).DeleteSku(ctx, sk.ID); err == nil {
				t.Fatal("referenced SKU was deleted")
			}
			if _, err := d.Client.ProductSku.Get(ctx, sk.ID); err != nil {
				t.Fatal("failed deletion removed the SKU", err)
			}
		})
	}
}

func TestDeleteUnusedSkuKeepsOtherSkuCards(t *testing.T) {
	d, _ := newStatsEnv(t)
	ctx := context.Background()
	p := d.Client.Product.Create().SetName("unrelated").SetSlug("unrelated").SetPrice(100).SetStatus(1).SaveX(ctx)
	a := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("monthly").SetSpecValues(map[string]string{}).SaveX(ctx)
	b := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("yearly").SetSpecValues(map[string]string{}).SaveX(ctx)
	d.Client.Card.Create().SetProductID(p.ID).SetSkuID(b.ID).SetContent([]byte("encrypted")).SetContentHash("year").SaveX(ctx)
	if err := NewProductRepoImpl(d, nil).DeleteSku(ctx, a.ID); err != nil {
		t.Fatal("unreferenced SKU should be deletable", err)
	}
	if d.Client.Card.Query().Where(card.SkuID(b.ID)).CountX(ctx) != 1 {
		t.Fatal("another SKU's stock changed")
	}
}
