package catalog

import (
	"context"
	"encoding/json"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestPhysicalPropertyAndOptionalSettings(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()
	physical, virtual := "physical", "virtual"
	falseValue := false
	req := &adminv1.CreateProductRequest{Name: "Physical settings", PriceCents: 100, StockType: "card", Status: 1, ProductProperty: &physical, SalesVisible: &falseValue, TrackInventory: &falseValue, ShippingCountries: []string{"US"}}
	p, err := svc.CreateProduct(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if p.GoodsType != physical || p.ProductProperty != physical || p.GetSalesVisible() || p.GetTrackInventory() {
		t.Fatalf("property/settings did not round trip: %+v", p)
	}
	encoded, err := protojson.Marshal(p)
	var payload map[string]any
	if err != nil || json.Unmarshal(encoded, &payload) != nil || payload["stockVisible"] != false || payload["salesVisible"] != false || payload["trackInventory"] != false {
		t.Fatalf("disabled settings disappeared from JSON: %s %v", encoded, err)
	}
	// An older admin only submits existing fields. It must preserve the new switches.
	_, err = svc.UpdateProduct(ctx, &adminv1.UpdateProductRequest{Id: p.Id, Name: "Legacy rename", PriceCents: 100, Status: 1})
	if err != nil {
		t.Fatal(err)
	}
	raw := d.Client.Product.GetX(ctx, p.Id)
	if raw.SalesVisible || raw.TrackInventory || raw.ProductProperty != physical {
		t.Fatal("legacy update reset physical settings")
	}
	if _, err = svc.UpdateProduct(ctx, &adminv1.UpdateProductRequest{Id: p.Id, ProductProperty: &virtual}); err == nil {
		t.Fatal("saved product changed property")
	}
	req.Name = "Conflict"
	req.GoodsType = &virtual
	if _, err = svc.CreateProduct(ctx, req); err == nil {
		t.Fatal("conflicting property and legacy goods type accepted")
	}
	legacy, err := svc.CreateProduct(ctx, &adminv1.CreateProductRequest{Name: "Legacy physical", PriceCents: 100, StockType: "card", GoodsType: &physical, ShippingCountries: []string{"US"}})
	if err != nil || legacy.ProductProperty != physical || !legacy.GetTrackInventory() || !legacy.GetSalesVisible() {
		t.Fatalf("legacy create did not mirror property or defaults: %+v %v", legacy, err)
	}
}

func TestDisabledProductDisplaySettingsRemainPresentInJSON(t *testing.T) {
	admin := ToAdminPB(&ent.Product{ID: 1, ProductProperty: "physical", GoodsType: "physical"})
	store := toStorefrontProduct(&port.Product{ID: 1, ProductProperty: "physical", GoodsType: "physical"}, map[uint64]int64{1: -1}, 4)
	for name, message := range map[string]proto.Message{"admin": admin, "storefront": store} {
		t.Run(name, func(t *testing.T) {
			for _, options := range []protojson.MarshalOptions{{}, {UseProtoNames: true}} {
				encoded, err := options.Marshal(message)
				var payload map[string]any
				if err != nil || json.Unmarshal(encoded, &payload) != nil {
					t.Fatal("encode product settings", err)
				}
				keys := []string{"stockVisible", "salesVisible", "trackInventory"}
				if options.UseProtoNames {
					keys = []string{"stock_visible", "sales_visible", "track_inventory"}
				}
				for _, key := range keys {
					if value, exists := payload[key]; !exists || value != false {
						t.Fatalf("disabled setting %s disappeared from product JSON: %s", key, encoded)
					}
				}
			}
		})
	}
}

func TestProductStockVisibilityUsesUpdatePresence(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()
	physical := "physical"
	p, err := svc.CreateProduct(ctx, &adminv1.CreateProductRequest{Name: "Display stock", PriceCents: 100, StockType: "card", Status: 1, ProductProperty: &physical, StockVisible: true, ShippingCountries: []string{"US"}})
	if err != nil {
		t.Fatal(err)
	}
	// Quick edits and older clients omit the setting and preserve true as well as false.
	if _, err = svc.UpdateProduct(ctx, &adminv1.UpdateProductRequest{Id: p.Id, PriceCents: 200, Status: 1}); err != nil || !d.Client.Product.GetX(ctx, p.Id).StockVisible {
		t.Fatal("omitted stock visibility reset a true preference", err)
	}
	off, on := false, true
	if _, err = svc.UpdateProduct(ctx, &adminv1.UpdateProductRequest{Id: p.Id, StockVisible: &off, PriceCents: 200, Status: 1}); err != nil || d.Client.Product.GetX(ctx, p.Id).StockVisible {
		t.Fatal("explicit false stock visibility did not persist", err)
	}
	if _, err = svc.UpdateProduct(ctx, &adminv1.UpdateProductRequest{Id: p.Id, PriceCents: 300, Status: 1}); err != nil || d.Client.Product.GetX(ctx, p.Id).StockVisible {
		t.Fatal("omitted stock visibility reset a false preference", err)
	}
	if _, err = svc.UpdateProduct(ctx, &adminv1.UpdateProductRequest{Id: p.Id, StockVisible: &on, PriceCents: 300, Status: 1}); err != nil || !d.Client.Product.GetX(ctx, p.Id).StockVisible {
		t.Fatal("explicit true stock visibility did not persist", err)
	}
}

func TestPhysicalRejectsHiddenDigitalContentBeforeCreation(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()
	physical := "physical"
	_, err := svc.CreateProduct(ctx, &adminv1.CreateProductRequest{Name: "Leaked link", PriceCents: 100, StockType: "url", ProductProperty: &physical, ShippingCountries: []string{"US"}, DirectContent: "https://example.com/hidden"})
	if err == nil || d.Client.Product.Query().CountX(ctx) != 0 {
		t.Fatal("hidden digital content created a physical product")
	}
	repo := svc.repo
	if _, err = repo.CreateProduct(ctx, port.ProductInput{Name: "Cipher leak", Price: 100, StockType: "card", GoodsType: &physical, ShippingCountries: []string{"US"}, DirectContent: []byte("ciphertext")}); err == nil {
		t.Fatal("repository accepted digital content for a physical product")
	}
	p := d.Client.Product.Create().SetName("Physical direct guard").SetSlug("physical-direct-guard").SetGoodsType(physical).SaveX(ctx)
	if err = repo.SetDirectContent(ctx, p.ID, []byte("ciphertext")); err == nil || len(d.Client.Product.GetX(ctx, p.ID).DirectContent) != 0 {
		t.Fatal("post-creation write accepted physical digital content")
	}
}

func TestPhysicalUnlimitedInventoryRetainsQuantities(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()
	repo := svc.repo
	p := d.Client.Product.Create().SetName("Inventory toggle").SetSlug("inventory-toggle").SetGoodsType("physical").SetPhysicalStock(5).SetShippingCountries([]string{"US"}).SaveX(ctx)
	falseValue, trueValue := false, true
	stale, hidden := int64(999), int64(1000)
	updated, err := repo.UpdateProduct(ctx, p.ID, port.ProductInput{TrackInventory: &falseValue, PhysicalStock: &hidden, ExpectedPhysicalStock: &stale})
	if err != nil || updated.TrackInventory || updated.PhysicalStock != 5 {
		t.Fatalf("disabling inventory altered frozen quantities: %+v %v", updated, err)
	}
	if updated.LockVersion <= p.LockVersion {
		t.Fatal("inventory mode change did not invalidate outstanding quotes")
	}
	version := updated.LockVersion
	if _, err = repo.CreateSku(ctx, SkuInput{ProductID: p.ID, Name: "Blocked topology", SpecValues: map[string]string{}, PhysicalStock: &hidden}); err == nil {
		t.Fatal("disabled inventory hid frozen base quantities behind a new SKU")
	}
	unchanged := d.Client.Product.GetX(ctx, p.ID)
	if unchanged.PhysicalStock != 5 || unchanged.TrackInventory || unchanged.LockVersion != version || d.Client.ProductSku.Query().CountX(ctx) != 0 {
		t.Fatal("failed SKU creation changed inventory, policy or quote version")
	}
	stocks, err := data.ProductStocks(ctx, d, []*ent.Product{updated})
	if err != nil || stocks[p.ID] != -1 {
		t.Fatalf("unlimited product stock: %v %v", stocks, err)
	}
	if _, err = repo.UpdateProduct(ctx, p.ID, port.ProductInput{TrackInventory: &trueValue}); err == nil {
		t.Fatal("inventory re-enabled without checking quantities")
	}
	target, expected := int64(8), int64(5)
	updated, err = repo.UpdateProduct(ctx, p.ID, port.ProductInput{TrackInventory: &trueValue, PhysicalStock: &target, ExpectedPhysicalStock: &expected})
	if err != nil || !updated.TrackInventory || updated.PhysicalStock != target {
		t.Fatalf("inventory not restored with checked stock: %+v %v", updated, err)
	}
	// SKU quantities remain frozen while management is disabled.
	p = d.Client.Product.Create().SetName("SKU toggle").SetSlug("sku-toggle").SetGoodsType("physical").SetTrackInventory(false).SetShippingCountries([]string{"US"}).SaveX(ctx)
	sk := d.Client.ProductSku.Create().SetProductID(p.ID).SetName("Size").SetSpecValues(map[string]string{}).SetPhysicalStock(3).SaveX(ctx)
	if _, err = repo.UpdateSku(ctx, sk.ID, SkuInput{PhysicalStock: &hidden, ExpectedPhysicalStock: &stale}); err != nil || d.Client.ProductSku.GetX(ctx, sk.ID).PhysicalStock != 3 {
		t.Fatal("disabled SKU inventory changed", err)
	}
	skus, err := repo.ListSkus(ctx, p.ID)
	if err != nil || len(skus) != 1 || skus[0].Stock != -1 {
		t.Fatalf("unlimited SKU stock: %+v %v", skus, err)
	}
}

func TestUpstreamSyncPreservesProductDisplaySettings(t *testing.T) {
	d, svc := newStatsEnv(t)
	ctx := context.Background()
	in := port.UpstreamProductInput{ConnectionID: 71, UpstreamProductCode: "display-options", Name: "Supplier product", Price: 100, Status: 1, AutoOnshelf: true}
	pid, _, err := svc.repo.UpsertUpstreamProduct(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	falseValue := false
	_, err = svc.UpdateProduct(ctx, &adminv1.UpdateProductRequest{Id: pid, SalesVisible: &falseValue, PriceCents: 100, Status: 1})
	if err != nil {
		t.Fatal(err)
	}
	in.Name, in.Price = "Supplier updated", 200
	if _, _, err = svc.repo.UpsertUpstreamProduct(ctx, in); err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpdateProduct(ctx, &adminv1.UpdateProductRequest{Id: pid, PriceCents: 300, Status: 1})
	if err != nil {
		t.Fatal(err)
	}
	p := d.Client.Product.GetX(ctx, pid)
	if p.SalesVisible || !p.TrackInventory || p.ProductProperty != "virtual" || p.GoodsType != "virtual" || p.Price != 300 || p.Name != in.Name {
		t.Fatalf("sync or quick update replaced display preferences: %+v", p)
	}
}
