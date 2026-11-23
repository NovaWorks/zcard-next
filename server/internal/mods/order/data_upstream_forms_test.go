package order

import (
	"context"
	"fmt"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/platform/purchaseform"
)

func TestUpstreamFormOrderSnapshotsSupplierKeys(t *testing.T) {
	for _, driver := range []string{"dujiao_next", "mcy_shop", "zcard"} {
		t.Run(driver, func(t *testing.T) { testUpstreamFormOrderSnapshotsSupplierKeys(t, driver) })
	}
}

func testUpstreamFormOrderSnapshotsSupplierKeys(t *testing.T, driver string) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	connection := d.Client.SupplyConnection.Create().SetName(driver).SetDriver(driver).SetBaseURL("https://example.test").SetCredentials([]byte("fixture")).SaveX(ctx)
	p := d.Client.Product.UpdateOneID(1).SetUpstreamSourceID(connection.ID).SetUpstreamProductCode("18").SaveX(ctx)
	f := d.Client.ProductControl.Create().SetProductID(p.ID).SetName("客户账号").SetType("text").SetRequired(true).SaveX(ctx)
	field := purchaseform.Field{Key: "x_handle", Type: "text", Required: true, MaxLength: 20, Regex: `^@[a-z]+$`}
	contract := purchaseform.Contract{ConnectionID: connection.ID, ProductCode: "18", Fields: []purchaseform.Binding{{ControlID: fmt.Sprint(f.ID), Field: field}}}
	d.Client.Product.UpdateOneID(p.ID).SetControlConfig(map[string]any{purchaseform.ConfigKey: contract}).ExecX(ctx)
	input := CreateOrderInput{UserID: 3, QueryPassword: "test1234", Items: []OrderItemInput{{ProductID: p.ID, Quantity: 1, ControlAnswers: map[string]string{fmt.Sprint(f.ID): "invalid"}}}}
	if _, err := uc.CreateOrder(ctx, input); err == nil {
		t.Fatal("invalid upstream form accepted before payment")
	}
	input.Items[0].ControlAnswers[fmt.Sprint(f.ID)] = "@alice"
	if _, err := uc.CreateOrder(ctx, input); err != nil {
		t.Fatal(err)
	}
	item := d.Client.OrderItem.Query().OnlyX(ctx)
	d.Client.ProductControl.UpdateOneID(f.ID).SetName("changed").ExecX(ctx)
	values, err := data.UpstreamFormSubmission(item.FormAnswers, connection.ID, "18")
	if err != nil || values["x_handle"] != "@alice" {
		t.Fatalf("snapshot did not preserve supplier key/value: %v", err)
	}
	if _, err := data.UpstreamFormSubmission(item.FormAnswers, connection.ID, "other"); err == nil {
		t.Fatal("forwarded private fields to a replacement product")
	}
	d.Client.ProductControl.UpdateOneID(f.ID).SetRequired(false).ExecX(ctx)
	input.Items[0].ControlAnswers[fmt.Sprint(f.ID)] = ""
	if _, err := uc.CreateOrder(ctx, input); err == nil {
		t.Fatal("operator settings weakened the supplier's required field")
	}
}

func TestMCYEmptyFormAllowsQuoteWithoutCreatingOrder(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	connection := d.Client.SupplyConnection.Create().SetName("MCY").SetDriver("mcy_shop").SetBaseURL("https://example.test").SetCredentials([]byte("fixture")).SaveX(ctx)
	contract := purchaseform.Contract{ConnectionID: connection.ID, ProductCode: "18", Fields: []purchaseform.Binding{}}
	p := d.Client.Product.UpdateOneID(1).SetUpstreamSourceID(connection.ID).SetUpstreamProductCode("18").SetControlConfig(map[string]any{purchaseform.ConfigKey: contract}).SaveX(ctx)
	input := CreateOrderInput{UserID: 3, QuoteOnly: true, QueryPassword: "test1234", Items: []OrderItemInput{{ProductID: p.ID, Quantity: 1}}}
	result, err := uc.CreateOrder(ctx, input)
	if err != nil || result.TotalCents != 500 || result.OrderNo != "" {
		t.Fatalf("MCY empty form quote: %+v %v", result, err)
	}
	if d.Client.Order.Query().CountX(ctx) != 0 {
		t.Fatal("quote persisted an order")
	}
	// Unrecognized drivers and changed supplier bindings must still be rejected.
	d.Client.SupplyConnection.UpdateOneID(connection.ID).SetDriver("acg_faka").ExecX(ctx)
	if _, err := uc.CreateOrder(ctx, input); err == nil {
		t.Fatal("unsupported driver accepted form contract")
	}
	d.Client.SupplyConnection.UpdateOneID(connection.ID).SetDriver("mcy_shop").ExecX(ctx)
	d.Client.Product.UpdateOneID(p.ID).SetUpstreamProductCode("changed").ExecX(ctx)
	if _, err := uc.CreateOrder(ctx, input); err == nil {
		t.Fatal("changed product accepted old form contract")
	}
}
