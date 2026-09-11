package order

import (
	"context"
	"strings"
	"testing"
)

func TestHiddenCategoryRejectsCheckoutAndRestores(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	parent := d.Client.Category.Create().SetName("隐藏父类").SetHide(true).SaveX(ctx)
	child := d.Client.Category.Create().SetName("子类").SetParentID(parent.ID).SaveX(ctx)
	d.Client.Product.UpdateOneID(1).SetCategoryID(child.ID).ExecX(ctx)
	in := CreateOrderInput{UserID: 3, QueryPassword: "test1234", Items: []OrderItemInput{{ProductID: 1, Quantity: 1}}}
	if _, err := uc.CreateOrder(ctx, in); err == nil || !strings.Contains(err.Error(), "PRODUCT_NOT_AVAILABLE") {
		t.Fatal(err)
	}
	if d.Client.Order.Query().CountX(ctx) != 0 {
		t.Fatal("rejected checkout created order")
	}
	d.Client.Category.UpdateOneID(parent.ID).SetHide(false).ExecX(ctx)
	if _, err := uc.CreateOrder(ctx, in); err != nil {
		t.Fatal(err)
	}
}
