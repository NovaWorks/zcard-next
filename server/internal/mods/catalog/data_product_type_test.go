package catalog

import (
	"context"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestPhysicalProductShippingCollectRoundTrip(t *testing.T) {
	_, svc := newStatsEnv(t)
	ctx := context.Background()
	product, err := svc.CreateProduct(ctx, &adminv1.CreateProductRequest{
		Name: "Parcel", PriceCents: 500, Status: 1, ProductProperty: proto.String("physical"),
		ShippingMode: proto.String("collect"), ShippingFeeCents: proto.Int64(300),
		ShippingCountries: []string{"US"},
	})
	if err != nil || product.ShippingMode != "collect" || product.ShippingFeeCents != 0 {
		t.Fatalf("create collect shipping: %+v, %v", product, err)
	}
	for _, change := range []struct {
		mode string
		fee  int64
		want int64
	}{
		{"fixed", 200, 200},
		{"collect", 200, 0},
		{"free", 200, 0},
	} {
		updated, err := svc.UpdateProduct(ctx, &adminv1.UpdateProductRequest{
			Id: product.Id, ShippingMode: proto.String(change.mode),
			ShippingFeeCents: proto.Int64(change.fee), ShippingCountries: []string{"US"}, Status: 1,
		})
		if err != nil || updated.ShippingMode != change.mode || updated.ShippingFeeCents != change.want {
			t.Fatalf("update shipping to %s: %+v, %v", change.mode, updated, err)
		}
	}
}

func TestUpdateProductStockTypeRoundTrip(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()
	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	svc.cipher = cipher
	p, err := svc.CreateProduct(ctx, &adminv1.CreateProductRequest{Name: "link product", StockType: "url", PriceCents: 100, Status: 1, StockVisible: true, DirectContent: "https://example.com/delivery"})
	if err != nil {
		t.Fatal(err)
	}
	req := &adminv1.UpdateProductRequest{}
	if err := protojson.Unmarshal([]byte(`{"stock_type":"card","name":"card product","price_cents":100,"status":1,"stock_visible":true,"direct_content":"ignored old editor value"}`), req); err != nil {
		t.Fatal(err)
	}
	req.Id = p.Id
	updated, err := svc.UpdateProduct(ctx, req)
	if err != nil || updated.StockType != "card" {
		t.Fatalf("update: %+v %v", updated, err)
	}
	reloaded, err := svc.GetProduct(ctx, &adminv1.GetProductRequest{Id: p.Id})
	if err != nil || reloaded.StockType != "card" || reloaded.Stock != 0 {
		t.Fatalf("reopen: %+v %v", reloaded, err)
	}
	raw := d.Client.Product.GetX(ctx, p.Id)
	// 已交付链接订单仍能解密原内容，新商品库存则按卡池计算。
	plain, err := cipher.Open(raw.DirectContent, p.Id, 0)
	if err != nil || plain != "https://example.com/delivery" {
		t.Fatalf("historical content changed: %v", err)
	}
	d.Client.Card.Create().SetProductID(p.Id).SetContent([]byte("encrypted-card")).SetContentHash("one").SetStatus("available").SaveX(ctx)
	reloaded, err = svc.GetProduct(ctx, &adminv1.GetProductRequest{Id: p.Id})
	if err != nil || reloaded.Stock != 1 {
		t.Fatal("card pool not used", err)
	}
	// 旧客户端省略类型仍可保存其他字段。
	_, err = svc.UpdateProduct(ctx, &adminv1.UpdateProductRequest{Id: p.Id, Name: "renamed", PriceCents: 100, Status: 1})
	if err != nil || d.Client.Product.GetX(ctx, p.Id).StockType != "card" {
		t.Fatal("omitted type changed product", err)
	}
	for _, kind := range []string{"invalid", "url", "code"} {
		_, err = svc.UpdateProduct(ctx, &adminv1.UpdateProductRequest{Id: p.Id, StockType: kind, Status: 1})
		if err == nil {
			t.Fatalf("accepted invalid/incomplete change %s", kind)
		}
	}
	_, err = svc.UpdateProduct(tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 9}), &adminv1.UpdateProductRequest{Id: p.Id, StockType: "code", DirectContent: "secret"})
	if err == nil {
		t.Fatal("cross subsite update allowed")
	}
	_, err = svc.UpdateProduct(ctx, &adminv1.UpdateProductRequest{Id: p.Id, StockType: "code", DirectContent: "REDEEM-123", Status: 1})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err = svc.GetProduct(ctx, &adminv1.GetProductRequest{Id: p.Id})
	if err != nil || reloaded.StockType != "code" || reloaded.Stock != -1 {
		t.Fatal("direct type not updated", err)
	}
	raw = d.Client.Product.GetX(ctx, p.Id)
	plain, err = cipher.Open(raw.DirectContent, p.Id, 0)
	if err != nil || plain != "REDEEM-123" {
		t.Fatal("new direct content not encrypted", err)
	}
}
