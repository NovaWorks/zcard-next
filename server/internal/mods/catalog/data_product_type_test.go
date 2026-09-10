package catalog

import (
	"context"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"google.golang.org/protobuf/encoding/protojson"
)

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
