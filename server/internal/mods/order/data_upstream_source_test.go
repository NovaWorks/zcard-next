package order

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/platform/upstreamsource"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestCreateOrderFreezesPrivateUpstreamSource(t *testing.T) {
	d, uc, pid := newGateEnv(t)
	ctx := context.Background()
	c := d.Client.SupplyConnection.Create().SetID(9).SetName("私有原渠道").SetDriver("mcy_shop").SetBaseURL("https://original-private.example").SetCredentials([]byte("secret")).SaveX(ctx)
	d.Client.Product.UpdateOneID(pid).SetUpstreamProductCode("12").ExecX(ctx)
	sku := d.Client.ProductSku.Create().SetProductID(pid).SetName("一年版").SetSpecValues(map[string]string{}).SetUpstreamSkuID("52").SaveX(ctx)
	uc.SetStockGate(&fakeStockGate{})
	created, e := uc.CreateOrder(ctx, CreateOrderInput{UserID: 3, QueryPassword: "test1234", Items: []OrderItemInput{{ProductID: pid, SkuID: sku.ID, Quantity: 1}}})
	if e != nil {
		t.Fatal(e)
	}
	it := d.Client.OrderItem.Query().OnlyX(ctx)
	if it.UpstreamSourceSnapshot["product_url"] != "https://original-private.example/item?id=12" || it.UpstreamSourceSnapshot["sku_id"] != "52" {
		t.Fatal(it.UpstreamSourceSnapshot)
	}
	d.Client.Product.UpdateOneID(pid).SetUpstreamSourceID(888).SetUpstreamProductCode("changed").ExecX(ctx)
	d.Client.SupplyConnection.DeleteOneID(c.ID).ExecX(ctx)
	admin, e := NewAdminOrderService(uc, d).GetOrder(ctx, &adminv1.GetAdminOrderRequest{OrderNo: created.OrderNo})
	if e != nil {
		t.Fatal(e)
	}
	if admin.Items[0].GetUpstreamSource().GetFields()["product_id"].GetStringValue() != "12" {
		t.Fatal(admin)
	}
	buyerCtx := identity.WithClaims(ctx, &authn.Claims{Subject: 3, Realm: authn.RealmUser})
	buyer, e := NewStoreOrderService(uc, nil).GetOrder(buyerCtx, &storefrontv1.GetOrderRequest{OrderNo: created.OrderNo})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := protojson.Marshal(buyer)
	for _, private := range []string{"original-private.example", "私有原渠道", "upstream_source", "upstreamSource"} {
		if strings.Contains(string(b), private) {
			t.Fatal("source leaked to buyer", string(b))
		}
	}
}

func TestCreateACGOrderFreezesWebPageAndDockingCode(t *testing.T) {
	d, uc, pid := newGateEnv(t)
	ctx := context.Background()
	d.Client.SupplyConnection.Create().SetID(9).SetName("private-acg").SetDriver("acg_faka").SetBaseURL("https://acg-private.example").SetCredentials([]byte("secret")).SaveX(ctx)
	d.Client.Product.UpdateOneID(pid).SetUpstreamProductCode("CA814E42E563538A").SetUpstreamNavigation(upstreamsource.Navigation(9, "CA814E42E563538A", "1111")).ExecX(ctx)
	uc.SetStockGate(&fakeStockGate{})
	_, e := uc.CreateOrder(ctx, CreateOrderInput{UserID: 3, QueryPassword: "test1234", Items: []OrderItemInput{{ProductID: pid, Quantity: 1}}})
	if e != nil {
		t.Fatal(e)
	}
	it := d.Client.OrderItem.Query().OnlyX(ctx)
	if it.UpstreamSourceSnapshot["product_url"] != "https://acg-private.example/item/1111" || it.UpstreamSourceSnapshot["product_code"] != "CA814E42E563538A" {
		t.Fatal(it.UpstreamSourceSnapshot)
	}
}
