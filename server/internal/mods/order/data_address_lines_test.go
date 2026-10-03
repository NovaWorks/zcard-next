package order

import (
	"context"
	"testing"
)

func TestPhysicalAddressSecondLinePersistsAndReplays(t *testing.T) {
	ctx := context.Background()
	d, uc := seedPhysical(t)
	in := physicalInput()
	in.IdempotencyKey = "second-address-line"
	in.ShippingAddress["address_line2"] = "  Apt. 4  "
	in = quotePhysical(t, uc, in)
	first, err := uc.CreateOrder(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Client.Order.Query().OnlyX(ctx).ShippingAddress["address_line2"]; got != "Apt. 4" {
		t.Fatalf("optional address line not normalized and persisted: %q", got)
	}
	again, err := uc.CreateOrder(ctx, in)
	if err != nil || again.OrderNo != first.OrderNo || d.Client.Order.Query().CountX(ctx) != 1 {
		t.Fatalf("address second line changed idempotent replay: %v", err)
	}
}
