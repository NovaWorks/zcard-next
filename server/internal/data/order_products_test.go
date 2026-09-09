package data

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"strings"
	"testing"
)

func TestOrderProductSummaries(t *testing.T) {
	d := newTestData(t)
	ctx := context.Background()
	a := d.Client.Order.Create().SetOrderNo("main").SaveX(ctx)
	b := d.Client.Order.Create().SetOrderNo("other-tenant").SetSubsiteID(8).SaveX(ctx)
	p := d.Client.Product.Create().SetName("会员月卡").SetSlug("monthly").SaveX(ctx)
	add := func(o *ent.Order, pid uint64, sku string, q int32) {
		d.Client.OrderItem.Create().SetOrderID(o.ID).SetSubsiteID(o.SubsiteID).SetProductID(pid).SetSkuID(3).SetSkuName(sku).SetQuantity(q).SetUnitPrice(100).SetAmount(100 * int64(q)).SetFulfillmentType("auto").SaveX(ctx)
	}
	add(a, p.ID, "一年版", 2)
	add(a, 9999, "已删除规格", 1)
	add(b, p.ID, "不应出现", 9)
	got, err := OrderProductSummaries(ctx, d, []*ent.Order{a})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[a.ID]) != 2 {
		t.Fatal(got)
	}
	first := got[a.ID][0]
	if first.Name != "会员月卡" || first.SkuName != "一年版" || first.Quantity != 2 || first.SkuID != 3 {
		t.Fatal(first)
	}
	if !strings.Contains(got[a.ID][1].Name, "9999") {
		t.Fatal("missing product fallback", got)
	}
	if _, ok := got[b.ID]; ok {
		t.Fatal("unrequested tenant order leaked")
	}
	got, err = OrderProductSummaries(ctx, nil, nil)
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	if err = d.Client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = OrderProductSummaries(ctx, d, []*ent.Order{a}); err == nil {
		t.Fatal("query failure swallowed")
	}
}
