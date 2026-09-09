package identity

import (
	"context"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"testing"
)

func TestMemberRecentOrderProducts(t *testing.T) {
	_, d := newRegCodeEnv(t)
	ctx := context.Background()
	u := d.Client.User.Create().SetUsername("buyer").SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("recent-product").SetUserID(u.ID).SaveX(ctx)
	p := d.Client.Product.Create().SetName("游戏点卡").SetSlug("recent-card").SaveX(ctx)
	d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetSkuName("100 点").SetQuantity(3).SetUnitPrice(100).SetAmount(300).SetFulfillmentType("auto").SaveX(ctx)
	svc := NewAdminUserManageService(&UserRepo{data: d}, nil)
	result, err := svc.GetUser(ctx, &adminv1.GetUserRequest{Id: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RecentOrders) != 1 || len(result.RecentOrders[0].Items) != 1 {
		t.Fatal(result)
	}
	item := result.RecentOrders[0].Items[0]
	if item.Name != "游戏点卡" || item.SkuName != "100 点" || item.Quantity != 3 {
		t.Fatal(item)
	}
}
