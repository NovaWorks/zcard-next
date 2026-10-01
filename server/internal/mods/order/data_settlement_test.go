package order

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
)

func TestCreateOrderSnapshotsConfiguredAUD(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	// This fixture creates its catalog directly; configure the accounting unit
	// before exercising the real order workflow, as an AUD installation would.
	d.Client.Setting.Create().SetGroup("i18n").SetKey("base_currency").SetValue(json.RawMessage(`"AUD"`)).SaveX(ctx)
	created, err := uc.CreateOrder(ctx, CreateOrderInput{QueryPassword: "test1234", UserID: 3, Items: []OrderItemInput{{ProductID: 1, Quantity: 2}}, IdempotencyKey: "aud-order"})
	if err != nil {
		t.Fatal(err)
	}
	o := d.Client.Order.Query().Where(order.OrderNo(created.OrderNo)).OnlyX(ctx)
	if o.BaseCurrency != "AUD" || o.TotalAmount != 1000 {
		t.Fatalf("order changed price or lost currency: %+v", o)
	}
	if _, err := uc.CreateOrder(ctx, CreateOrderInput{QueryPassword: "test1234", UserID: 3, Items: []OrderItemInput{{ProductID: 1, Quantity: 2}}, IdempotencyKey: "aud-order"}); err != nil {
		t.Fatal(err)
	}
	if d.Client.Order.Query().CountX(ctx) != 1 {
		t.Fatal("idempotent AUD order duplicated")
	}
}
