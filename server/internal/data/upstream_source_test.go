package data

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
)

func TestUpstreamSourcesFreezeAndLegacyRecovery(t *testing.T) {
	d := newTestData(t)
	ctx := context.Background()
	c := d.Client
	conn := c.SupplyConnection.Create().SetName("原供应商").SetDriver("mcy_shop").SetBaseURL("https://user:secret@old.example?token=secret").SetCredentials([]byte("secret")).SaveX(ctx)
	p := c.Product.Create().SetName("原商品").SetSlug("source").SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("12~52").SaveX(ctx)
	o := c.Order.Create().SetOrderNo("source-test").SaveX(ctx)
	orderSource, e := CaptureUpstreamSource(ctx, c, conn.ID, p.UpstreamProductCode, "", "order")
	if e != nil {
		t.Fatal(e)
	}
	it := c.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(100).SetAmount(100).SetFulfillmentType("upstream").SetUpstreamSourceSnapshot(orderSource).SaveX(ctx)
	// Another item without a historical record must not inherit today's mapping.
	unknown := c.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(100).SetAmount(100).SetFulfillmentType("upstream").SaveX(ctx)
	purchaseSource, e := CaptureUpstreamSource(ctx, c, conn.ID, "13~53", "", "purchase")
	if e != nil {
		t.Fatal(e)
	}
	po := c.ProcurementOrder.Create().SetOrderItemID(it.ID).SetConnectionID(conn.ID).SetDedupeKey("source-po").SetUpstreamSourceSnapshot(purchaseSource).SaveX(ctx)
	c.ProcurementItem.Create().SetProcurementID(po.ID).SetUpstreamSku("13~53").SetQuantity(1).SaveX(ctx)
	legacy := c.ProcurementOrder.Create().SetOrderItemID(unknown.ID).SetConnectionID(conn.ID).SetDedupeKey("legacy-po").SaveX(ctx)
	c.ProcurementItem.Create().SetProcurementID(legacy.ID).SetUpstreamSku("14~54").SetQuantity(1).SaveX(ctx)
	c.Product.UpdateOneID(p.ID).SetUpstreamSourceID(999).SetUpstreamProductCode("changed").ExecX(ctx)
	c.SupplyConnection.UpdateOneID(conn.ID).SetName("改名").SetBaseURL("https://new.example").ExecX(ctx)
	sources, e := OrderItemSources(ctx, c, []*ent.OrderItem{it, unknown})
	if e != nil {
		t.Fatal(e)
	}
	if s := sources[it.ID]; s["product_url"] != "https://old.example/item?id=13" || s["sku_id"] != "53" || s["connection_name"] != "原供应商" || s["stage"] != "purchase" {
		t.Fatal(s)
	}
	if s := sources[unknown.ID]; s["product_id"] != "14" || s["stage"] != "legacy" || s["product_url"] != "https://new.example/item?id=14" {
		t.Fatal(s)
	}
	c.SupplyConnection.DeleteOneID(conn.ID).ExecX(ctx)
	sources, e = OrderItemSources(ctx, c, []*ent.OrderItem{it, unknown})
	if e != nil {
		t.Fatal(e)
	}
	if sources[it.ID]["product_url"] != "https://old.example/item?id=13" {
		t.Fatal("deleted source lost snapshot", sources)
	}
	if s := sources[unknown.ID]; s["product_url"] != "" || s["product_id"] != "14~54" {
		t.Fatal("invented legacy URL", s)
	}
	c.ProcurementOrder.DeleteOneID(po.ID).ExecX(ctx)
	sources, e = OrderItemSources(ctx, c, []*ent.OrderItem{it})
	if e != nil || sources[it.ID]["product_url"] != "https://old.example/item?id=12" {
		t.Fatal("order snapshot lost", sources, e)
	}
	c.ProcurementOrder.DeleteOneID(legacy.ID).ExecX(ctx)
	sources, e = OrderItemSources(ctx, c, []*ent.OrderItem{unknown})
	if e != nil || sources[unknown.ID]["kind"] != "unknown" {
		t.Fatal("legacy order retargeted", sources, e)
	}
	b, _ := json.Marshal(orderSource)
	if strings.Contains(string(b), "secret") {
		t.Fatal("credentials leaked", string(b))
	}
}
