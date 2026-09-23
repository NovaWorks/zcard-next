package supply

import (
	"context"
	"log/slog"
	"math"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestConnectionPricingPatch(t *testing.T) {
	repo, d := newTestRepo(t)
	svc := NewAdminSupplyService(repo, nil)
	ctx := context.Background()
	c, err := svc.CreateConnection(ctx, &adminv1.CreateConnectionRequest{Name: "pricing", Driver: "zcard", BaseUrl: "https://8.8.8.8", Credentials: "{}", ExchangeRate: 1.5, PriceMarkupPercent: 30, PriceMarkupAmount: 500, AutoSyncPrice: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.PriceMarkupAmount != 500 || d.Client.SupplyConnection.GetX(ctx, c.Id).PriceMarkupAmount != 500 {
		t.Fatal("fixed markup lost on create/read")
	}
	// A schedule-only PATCH must not reset exchange rate, markup, or sync switch.
	c, err = svc.UpdateConnection(ctx, &adminv1.UpdateConnectionRequest{Id: c.Id, Settings: `{"schedule":{"enabled":true}}`})
	if err != nil {
		t.Fatal(err)
	}
	if c.ExchangeRate != 1.5 || c.PriceMarkupPercent != 30 || c.PriceMarkupAmount != 500 || !c.AutoSyncPrice {
		t.Fatalf("partial update reset pricing: %v", c)
	}
	if !d.Client.SupplyConnection.GetX(ctx, c.Id).Settings["schedule"].(map[string]any)["enabled"].(bool) {
		t.Fatal("settings not saved")
	}
	// Exercise the actual JSON presence semantics used by the browser.
	req := &adminv1.UpdateConnectionRequest{}
	if err := protojson.Unmarshal([]byte(`{"price_markup_percent":0,"price_markup_amount":0,"auto_sync_price":false,"settings":"{}"}`), req); err != nil {
		t.Fatal(err)
	}
	req.Id = c.Id
	c, err = svc.UpdateConnection(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	stored := d.Client.SupplyConnection.GetX(ctx, c.Id)
	if c.ExchangeRate != 1.5 || c.PriceMarkupPercent != 0 || c.PriceMarkupAmount != 0 || c.AutoSyncPrice || stored.AutoSyncPrice || len(stored.Settings) != 0 {
		t.Fatalf("explicit zero/false lost: %v", c)
	}
	// Independent updates must not overwrite other fields.
	_, err = repo.UpdateConnection(ctx, c.Id, &ConnectionUpdate{SupplyConnection: stored, PriceMarkupAmount: proto.Int64(250)})
	if err != nil {
		t.Fatal(err)
	}
	c, err = svc.UpdateConnection(ctx, &adminv1.UpdateConnectionRequest{Id: c.Id, Name: "renamed"})
	if err != nil {
		t.Fatal(err)
	}
	if c.PriceMarkupAmount != 250 || c.AutoSyncPrice {
		t.Fatal("omitted fields changed")
	}
	for _, bad := range []*adminv1.UpdateConnectionRequest{
		{ExchangeRate: proto.Float64(0)}, {ExchangeRate: proto.Float64(-1)}, {ExchangeRate: proto.Float64(math.NaN())},
		{PriceMarkupPercent: proto.Float64(-1)}, {PriceMarkupPercent: proto.Float64(math.Inf(1))},
		{PriceMarkupAmount: proto.Int64(-1)}, {PriceRoundingMode: "invalid"}, {Settings: "[]"},
	} {
		bad.Id = c.Id
		if _, err := svc.UpdateConnection(ctx, bad); err == nil {
			t.Fatalf("accepted invalid settings: %v", bad)
		}
	}
}

func TestImportChannelPricingAndPriceOnlySKU(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "channel")
	sealed, err := repo.SealCredentials("dujiao_next", "https://8.8.8.8", `{"api_key":"fixture","api_secret":"fixture"}`)
	if err != nil {
		t.Fatal(err)
	}
	conn = d.Client.SupplyConnection.UpdateOneID(conn.ID).SetDriver("dujiao_next").SetBaseURL("https://8.8.8.8").SetCredentials(sealed).SetExchangeRate(1.5).SetPriceMarkupPercent(30).SetPriceMarkupAmount(500).SaveX(ctx)
	writer := catalog.NewProductRepoImpl(d, nil)
	syncSvc := &SyncService{repo: repo, writer: writer, maintainer: writer, log: slog.Default()}
	svc := NewAdminSupplyService(repo, syncSvc)
	upstream := adapter.Product{ID: "P", Name: "product", Price: 1000, IsActive: true, Stock: 8, SKUs: []adapter.SKU{{ID: "S", Code: "S", Name: "sku", Price: 2000, Stock: 8, IsActive: true, SpecValues: map[string]string{"size": "S"}}}}
	previewCache.Lock()
	previewCache.m[conn.ID] = previewEntry{at: time.Now(), byCode: map[string]adapter.Product{"P": upstream}}
	previewCache.Unlock()
	t.Cleanup(func() { previewCache.Lock(); delete(previewCache.m, conn.ID); previewCache.Unlock() })
	req := &adminv1.ImportProductsRequest{ConnectionId: conn.ID, Codes: []string{"P"}}
	importProducts := func() (*adminv1.ImportProductsReply, error) {
		previewCache.Lock()
		previewCache.m[conn.ID] = previewEntry{at: time.Now(), byCode: map[string]adapter.Product{"P": upstream}}
		previewCache.Unlock()
		return svc.ImportProducts(ctx, req)
	}

	if _, err := importProducts(); err != nil {
		t.Fatal(err)
	}
	m, err := repo.GetMapping(ctx, conn.ID, "P", "")
	if err != nil {
		t.Fatal(err)
	}
	sku := d.Client.ProductSku.Query().Where(productsku.ProductID(m.LocalProductID)).OnlyX(ctx)
	if d.Client.Product.GetX(ctx, m.LocalProductID).Price != 2450 || sku.Price != 4400 {
		t.Fatalf("channel import missed composite pricing: sku=%d", sku.Price)
	}
	// Explicit import rules remain independent, including explicitly zero percent.
	req.PricingMode = "percent"
	req.MarkupPercent = 0
	if _, err := importProducts(); err != nil {
		t.Fatal(err)
	}
	if d.Client.Product.GetX(ctx, m.LocalProductID).Price != 1500 || d.Client.ProductSku.GetX(ctx, sku.ID).Price != 3000 {
		t.Fatal("explicit zero import was replaced by channel markup")
	}
	// An existing saved import default is preserved; channel can be saved explicitly.
	d.Client.SupplyConnection.UpdateOneID(conn.ID).SetSettings(map[string]any{"import_pricing": map[string]any{"mode": "fixed", "markup_amount_cents": 200}}).ExecX(ctx)
	req.PricingMode = ""
	if _, err := importProducts(); err != nil {
		t.Fatal(err)
	}
	if d.Client.Product.GetX(ctx, m.LocalProductID).Price != 1700 {
		t.Fatal("legacy import preference not preserved")
	}
	req.PricingMode = "channel"
	req.SaveDefault = true
	if _, err := importProducts(); err != nil {
		t.Fatal(err)
	}
	if d.Client.SupplyConnection.GetX(ctx, conn.ID).Settings["import_pricing"].(map[string]any)["mode"] != "channel" {
		t.Fatal("channel preference not saved")
	}
	manual := d.Client.ProductSku.Create().SetProductID(m.LocalProductID).SetName("local only").SetSpecValues(map[string]string{"size": "L"}).SetPrice(888).SaveX(ctx)
	other := d.Client.Product.Create().SetName("other").SetSlug("other").SaveX(ctx)
	otherSKU := d.Client.ProductSku.Create().SetProductID(other.ID).SetName("other sku").SetSpecValues(map[string]string{}).SetUpstreamSkuID("S").SetPrice(777).SaveX(ctx)
	conn.PriceMarkupPercent = 40
	m, _ = repo.GetMapping(ctx, conn.ID, "P", "")
	if err := syncSvc.syncPriceOnly(ctx, conn, m, &upstream, false, &TaskProgress{}); err != nil {
		t.Fatal(err)
	}
	if d.Client.Product.GetX(ctx, m.LocalProductID).Price != 2600 || d.Client.ProductSku.GetX(ctx, sku.ID).Price != 4700 {
		t.Fatal("price-only sync missed SKU price")
	}
	if d.Client.ProductSku.GetX(ctx, manual.ID).Price != 888 || d.Client.ProductSku.GetX(ctx, otherSKU.ID).Price != 777 {
		t.Fatal("changed local or other product SKU")
	}
	// Manual product price protection persists across repeated syncs, including SKU prices.
	d.Client.Product.UpdateOneID(m.LocalProductID).SetPrice(9999).ExecX(ctx)
	for i := 0; i < 2; i++ {
		m, _ = repo.GetMapping(ctx, conn.ID, "P", "")
		if err := syncSvc.syncPriceOnly(ctx, conn, m, &upstream, false, &TaskProgress{}); err != nil {
			t.Fatal(err)
		}
	}
	if d.Client.Product.GetX(ctx, m.LocalProductID).Price != 9999 || d.Client.ProductSku.GetX(ctx, sku.ID).Price != 4700 {
		t.Fatal("repeat sync overwrote manual price")
	}
	m, _ = repo.GetMapping(ctx, conn.ID, "P", "")
	if err := syncSvc.syncPriceOnly(ctx, conn, m, &upstream, true, &TaskProgress{}); err != nil {
		t.Fatal(err)
	}
	if d.Client.Product.GetX(ctx, m.LocalProductID).Price != 2600 {
		t.Fatal("force reprice failed")
	}
	req.PricingMode = "invalid"
	if _, err := importProducts(); err == nil {
		t.Fatal("invalid mode accepted")
	}
}
