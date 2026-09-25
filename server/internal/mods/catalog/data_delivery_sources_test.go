package catalog

import (
	"context"
	"encoding/base64"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"testing"
)

func TestDeliverySourceConfigurationGuardsAndVersions(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := context.Background()
	cipher, _ := inventory.NewCardCipher(make([]byte, 32))
	s.cipher = cipher
	p := d.Client.Product.Create().SetName("upstream").SetSlug("src").SetUpstreamSourceID(1).SetUpstreamProductCode("p").SaveX(ctx)
	sku := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("A").SetSpecValues(map[string]string{}).SetUpstreamSkuID("a").SaveX(ctx)
	other := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("B").SetSpecValues(map[string]string{}).SetUpstreamSkuID("b").SaveX(ctx)
	req := &adminv1.SetDeliverySourceRequest{ProductId: p.ID, SkuId: sku.ID, Mode: "reuse", Content: "my-account"}
	got, e := s.SetDeliverySource(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	source, e := data.CurrentDeliverySource(ctx, d.Client, p, sku.ID)
	if e != nil {
		t.Fatal(e)
	}
	if source.Status != "ready" || string(source.Content) == req.Content {
		t.Fatal("plaintext or unready")
	}
	if data.StockSource(p, d.Client.ProductSku.GetX(ctx, other.ID)) != "upstream" {
		t.Fatal("other SKU mode overwritten")
	}
	if _, e = s.repo.CreateProductControl(ctx, p.ID, 0, "个人账号", "text", true, nil, 0); e == nil {
		t.Fatal("personal form enabled on shared content")
	}
	if _, e = s.SetDeliverySource(ctx, req); e == nil {
		t.Fatal("stale revision accepted")
	}
	if _, e = s.repo.UpdateSku(ctx, sku.ID, SkuInput{FulfillmentMode: "auto"}); e == nil {
		t.Fatal("regular SKU editor bypassed source settings")
	}
	if _, e = s.repo.CreateSku(ctx, SkuInput{ProductID: p.ID, Name: "bad", FulfillmentMode: "reuse"}); e == nil {
		t.Fatal("unconfigured reusable SKU created")
	}
	req.ExpectedRevision = got.Revision
	req.Content = "new-account"
	if _, e = s.SetDeliverySource(ctx, req); e != nil {
		t.Fatal(e)
	}
	old := d.Client.ProductDeliverySource.GetX(ctx, source.ID)
	plain, e := cipher.Open(old.Content, p.ID, 0)
	if e != nil || string(plain) != "my-account" || old.CurrentKey != nil {
		t.Fatal("historical version changed")
	}
	fresh := d.Client.Product.GetX(ctx, p.ID)
	d.Client.Product.UpdateOneID(p.ID).SetIsLocked(true).ExecX(ctx)
	req.ExpectedRevision = fresh.LockVersion
	if _, e = s.SetDeliverySource(ctx, req); !data.IsProductLocked(e) {
		t.Fatal("locked source editable")
	}
}
func TestHistoricalReceiptRequiresExactSKU(t *testing.T) {
	d, s := newStatsEnv(t)
	ctx := context.Background()
	cipher, _ := inventory.NewCardCipher(make([]byte, 32))
	s.cipher = cipher
	p := d.Client.Product.Create().SetName("P").SetSlug("history").SetUpstreamSourceID(1).SaveX(ctx)
	a := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("A").SetSpecValues(map[string]string{}).SaveX(ctx)
	b := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("B").SetSpecValues(map[string]string{}).SaveX(ctx)
	o := d.Client.Order.Create().SetOrderNo("historical").SetStatus("delivered").SetTotalAmount(100).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetSkuID(a.ID).SetQuantity(1).SetUnitPrice(100).SetAmount(100).SetFulfillmentType("upstream").SaveX(ctx)
	po := d.Client.ProcurementOrder.Create().SetDedupeKey("history-test").SetOrderItemID(it.ID).SetConnectionID(1).SetStatus("fulfilled").SaveX(ctx)
	sealed, _ := cipher.Seal("history-account", p.ID, 0)
	d.Client.ProcurementItem.Create().SetProcurementID(po.ID).SetQuantity(1).SetReceivedContent([]string{base64.StdEncoding.EncodeToString(sealed)}).SaveX(ctx)
	req := &adminv1.SetDeliverySourceRequest{ProductId: p.ID, SkuId: b.ID, Mode: "reuse", ProcurementId: po.ID}
	if _, e := s.SetDeliverySource(ctx, req); e == nil {
		t.Fatal("cross SKU history accepted")
	}
	req.SkuId = a.ID
	d.Client.OrderItem.UpdateOneID(it.ID).SetFormAnswers([]map[string]string{{"name": "客户账号", "value": "private"}}).ExecX(ctx)
	if _, e := s.SetDeliverySource(ctx, req); e == nil {
		t.Fatal("customer-specific receipt reused")
	}
	d.Client.OrderItem.UpdateOneID(it.ID).ClearFormAnswers().ExecX(ctx)
	d.Client.Order.UpdateOneID(o.ID).SetStatus("refund_pending").ExecX(ctx)
	if _, e := s.SetDeliverySource(ctx, req); e == nil {
		t.Fatal("refunding receipt reused")
	}
	d.Client.Order.UpdateOneID(o.ID).SetStatus("delivered").ExecX(ctx)
	if _, e := s.SetDeliverySource(ctx, req); e != nil {
		t.Fatal(e)
	}
}
