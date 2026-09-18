package catalog

import (
	"context"
	"fmt"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

type optionsSoldCounter struct{ calls int }

func (c *optionsSoldCounter) SoldBatch(context.Context, []uint64) (map[uint64]int64, error) {
	c.calls++
	return nil, nil
}

func TestProductOptionsPaginationAndLegacyList(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()
	counter := &optionsSoldCounter{}
	svc.sold = counter
	for i := 1; i <= 107; i++ {
		d.Client.Product.Create().SetName(fmt.Sprintf("option-%03d", i)).SetSlug(fmt.Sprintf("option-%03d", i)).
			SetPrice(int64(i)).SetDescription("large rich description").SetStockType("url").SetStatus(1).SaveX(ctx)
	}
	d.Client.Product.Create().SetName("other tenant").SetSlug("other").SetSubsiteID(9).SetPrice(1).SaveX(ctx)
	d.Client.Product.Create().SetName("deleted").SetSlug("deleted").SetStatus(-1).SetPrice(1).SaveX(ctx)
	seen := map[uint64]bool{}
	for page := int32(1); page <= 2; page++ {
		reply, err := svc.ListProducts(ctx, &adminv1.ListProductsRequest{Page: page, PageSize: 100, OptionsOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		if reply.Total != 107 {
			t.Fatalf("total=%d", reply.Total)
		}
		for _, p := range reply.Products {
			if seen[p.Id] || p.Description != "" || p.StockType != "url" || p.PriceCents == 0 {
				t.Fatalf("invalid option: %+v", p)
			}
			seen[p.Id] = true
		}
	}
	if len(seen) != 107 || counter.calls != 0 {
		t.Fatalf("options=%d stats calls=%d", len(seen), counter.calls)
	}
	legacy, err := svc.ListProducts(ctx, &adminv1.ListProductsRequest{PageSize: 1})
	if err != nil || legacy.Products[0].Description != "large rich description" || legacy.Products[0].Stock != -1 || counter.calls != 1 {
		t.Fatalf("legacy list changed: %+v %v calls=%d", legacy, err, counter.calls)
	}
	sub, err := svc.ListProducts(tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 9}), &adminv1.ListProductsRequest{OptionsOnly: true})
	if err != nil || sub.Total != 1 || sub.Products[0].Name != "other tenant" {
		t.Fatalf("tenant options: %+v %v", sub, err)
	}
	filtered, err := svc.ListProducts(ctx, &adminv1.ListProductsRequest{OptionsOnly: true, Keyword: "option-107"})
	if err != nil || filtered.Total != 1 {
		t.Fatalf("keyword options: %+v %v", filtered, err)
	}
}
