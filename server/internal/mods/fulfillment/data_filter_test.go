package fulfillment

import (
	"context"
	"testing"
)

func TestDeliveryFilterBeforePagination(t *testing.T) {
	d, _, repo := newFulfillData(t)
	ctx := context.Background()
	o := d.Client.Order.Create().SetOrderNo("FILTER").SetTotalAmount(100).SaveX(ctx)
	for i := 0; i < 6; i++ {
		item := uint64(1)
		if i%2 == 0 {
			item = 2
		}
		d.Client.OrderDelivery.Create().SetOrderID(o.ID).SetItemID(item).SetCardID(0).SetDeliveredMode("status").SetDeliveredBy(0).SetDeliveryTokenHash(string(rune('a' + i))).SaveX(ctx)
	}
	first, total, err := repo.ListDeliveries(ctx, o.OrderNo, 1, 2, 2)
	if err != nil || total != 3 || len(first) != 2 {
		t.Fatalf("first: %d %d %v", total, len(first), err)
	}
	second, total, err := repo.ListDeliveries(ctx, o.OrderNo, 2, 2, 2)
	if err != nil || total != 3 || len(second) != 1 {
		t.Fatalf("second: %d %d %v", total, len(second), err)
	}
	for _, row := range append(first, second...) {
		if row.ItemID != 2 {
			t.Fatal("different product leaked into procurement delivery")
		}
	}
	if first[0].ID == second[0].ID || first[1].ID == second[0].ID {
		t.Fatal("pagination repeated a delivery")
	}
}
