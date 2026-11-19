package order

import (
	"context"
	"fmt"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/platform/purchaseform"
)

func TestUpstreamFormOrderSnapshotsSupplierKeys(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	connection := d.Client.SupplyConnection.Create().SetName("dujiao").SetDriver("dujiao_next").SetBaseURL("https://example.test").SetCredentials([]byte("fixture")).SaveX(ctx)
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
