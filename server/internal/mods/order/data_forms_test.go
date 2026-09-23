package order

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	entorder "github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"testing"
)

func TestServiceFormSnapshotsAndQuota(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	p := d.Client.Product.UpdateOneID(1).SetFulfillmentMode("manual").SetManualStock(2).SaveX(ctx)
	f := d.Client.ProductControl.Create().SetProductID(p.ID).SetName("频道链接").SetType("text").SetRequired(true).SetValidation("url").SaveX(ctx)
	input := CreateOrderInput{UserID: 3, QueryPassword: "test1234", Items: []OrderItemInput{{ProductID: p.ID, Quantity: 1}}}
	for _, answers := range []map[string]string{nil, {fmt.Sprint(f.ID): "bad"}, {fmt.Sprint(f.ID): "https://t.me/channel", "999": "foreign"}} {
		input.Items[0].ControlAnswers = answers
		if _, err := uc.CreateOrder(ctx, input); err == nil {
			t.Fatalf("accepted invalid answers: %v", answers)
		}
	}
	if d.Client.Order.Query().CountX(ctx) != 0 {
		t.Fatal("invalid forms persisted orders")
	}
	input.Items[0].ControlAnswers = map[string]string{fmt.Sprint(f.ID): "https://t.me/channel"}
	input.Items[0].Quantity = 2
	result, err := uc.CreateOrder(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	line := d.Client.OrderItem.Query().OnlyX(ctx)
	if line.FulfillmentType != "manual" || line.FormAnswers[0]["name"] != "频道链接" || line.FormAnswers[0]["value"] != "https://t.me/channel" {
		t.Fatalf("bad snapshot: %+v", line)
	}
	d.Client.ProductControl.UpdateOneID(f.ID).SetName("改名").ExecX(ctx)
	d.Client.Product.UpdateOneID(p.ID).SetName("改名商品").ExecX(ctx)
	if got := d.Client.OrderItem.GetX(ctx, line.ID); got.ProductName != p.Name || got.FormAnswers[0]["name"] != "频道链接" {
		t.Fatal("snapshot mutated")
	}
	if _, err = uc.CreateOrder(ctx, input); err == nil {
		t.Fatal("oversold manual quota")
	}
	o := d.Client.Order.Query().Where(entorder.OrderNo(result.OrderNo)).OnlyX(ctx)
	d.Client.Order.UpdateOneID(o.ID).SetStatus("canceled").ExecX(ctx)
	if _, err = uc.CreateOrder(ctx, input); err != nil {
		t.Fatalf("cancel did not free quota: %v", err)
	}
}

func TestPerSKUFormAnswersAndMixedStock(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	p := d.Client.Product.UpdateOneID(1).SetManualStock(4).SaveX(ctx)
	a := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("A").SetSpecValues(map[string]string{}).SetFulfillmentMode("manual").SaveX(ctx)
	b := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("B").SetSpecValues(map[string]string{}).SetFulfillmentMode("manual").SaveX(ctx)
	f := d.Client.ProductControl.Create().SetProductID(p.ID).SetName("用户名").SetType("text").SetRequired(true).SetValidation("username").SaveX(ctx)
	stocks, err := data.ProductStocks(ctx, d, []*ent.Product{p})
	if err != nil || stocks[p.ID] != 4 {
		t.Fatalf("manual SKU hidden by empty card stock: %v %v", stocks, err)
	}
	_, err = uc.CreateOrder(ctx, CreateOrderInput{UserID: 3, QueryPassword: "test1234", Items: []OrderItemInput{
		{ProductID: p.ID, SkuID: a.ID, Quantity: 1, ControlAnswers: map[string]string{fmt.Sprint(f.ID): "@alice"}},
		{ProductID: p.ID, SkuID: b.ID, Quantity: 1, ControlAnswers: map[string]string{fmt.Sprint(f.ID): "@bob"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	rows := d.Client.OrderItem.Query().Order(ent.Asc(orderitem.FieldID)).AllX(ctx)
	if len(rows) != 2 || rows[0].FormAnswers[0]["value"] != "@alice" || rows[1].FormAnswers[0]["value"] != "@bob" {
		t.Fatal("per-SKU answers mixed")
	}
}

func TestServiceAnswerValidation(t *testing.T) {
	for _, tc := range []struct {
		f     ent.ProductControl
		v     string
		valid bool
	}{
		{ent.ProductControl{Validation: "tron"}, "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb", true},
		{ent.ProductControl{Validation: "tron"}, "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwc", false},
		{ent.ProductControl{Type: "number"}, "NaN", false},
		{ent.ProductControl{Type: "checkbox", Options: []string{"A", "B"}}, "A,A", false},
		{ent.ProductControl{Type: "select", Options: []string{"A", "B"}}, "C", false},
		{ent.ProductControl{Validation: "url"}, "javascript:alert(1)", false},
		{ent.ProductControl{MaxLength: 2}, "中文三", false},
	} {
		if (validateAnswer(&tc.f, tc.v) == nil) != tc.valid {
			t.Errorf("unexpected validation for %q", tc.v)
		}
	}
}
