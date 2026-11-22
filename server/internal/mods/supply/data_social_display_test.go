package supply

import (
	"encoding/json"
	"log/slog"
	"testing"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

func TestSocialCatalogRetailDisplayAndDescription(t *testing.T) {
	n, d, provider, ctx, p := nativeSocialEnv(t)
	writer := catalog.NewProductRepoImpl(d, nil)
	syncer := NewSyncService(n.g.repo, writer, writer, nil, nil, nil, slog.Default())
	raw, _ := json.Marshal(provider.schema)
	var rules map[string]any
	_ = json.Unmarshal(raw, &rules)
	_, err := syncer.ImportOne(ctx, n.conn, &adapter.Product{ID: "1", Name: "Service", ProductKind: "social_service", DeliveryKind: "social_service", IsActive: true, Stock: -2, SocialSchema: rules, Description: "<p>Service instructions</p><script>alert(1)</script>", DescriptionSet: true}, nil, PriceModeChannel, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	store := catalog.NewStoreCatalogService(catalog.NewCatalogUsecase(writer), nil, nil)
	store.SetStockLookup(n.g)
	// A historical SKU mapping must not replace the service's independent pricing rule.
	d.Client.SupplyMapping.Create().SetConnectionID(n.conn.ID).SetUpstreamProduct("1").SetUpstreamSku("legacy-sku").SetLocalProductID(p.ID).SetPricingOverride(map[string]any{"rule": map[string]any{"mode": PriceModePending}}).SaveX(ctx)
	check := func(want float64) {
		t.Helper()
		detail, e := store.GetProduct(ctx, &storefrontv1.GetProductRequest{Id: p.ID})
		if e != nil {
			t.Fatal(e)
		}
		list, e := store.ListProducts(ctx, &storefrontv1.ListProductsRequest{Page: 1, PageSize: 20})
		if e != nil || len(list.Items) != 1 {
			t.Fatal(list, e)
		}
		for _, item := range []*storefrontv1.Product{detail, list.Items[0]} {
			if item.PriceCents != 0 || item.Description != "<p>Service instructions</p>" {
				t.Fatal("description or card price", item)
			}
			schema := item.SocialSchema.AsMap()
			if _, exposed := schema["billing"].(map[string]any)["rate"]; exposed {
				t.Fatal("cost leaked")
			}
			price, present := schema["display_price"].(map[string]any)
			if want == 0 {
				if present {
					t.Fatal("unavailable pricing displayed", price)
				}
				continue
			}
			if !present || price["amount_cents"] != want || price["quantity"] != float64(1000) || price["currency"] != "CNY" {
				t.Fatal("retail sample", price)
			}
		}
	}
	check(13) // USD .015 × 7.2, ceiling to 11 cents, plus 2 cents retail markup.
	d.Client.SupplyConnection.UpdateOneID(n.conn.ID).SetPriceMarkupAmount(9).ExecX(ctx)
	check(20) // Display follows current rules without another provider call/import.
	m, e := n.g.repo.GetMapping(ctx, n.conn.ID, "1", "")
	if e != nil {
		t.Fatal(e)
	}
	d.Client.SupplyMapping.UpdateOneID(m.ID).SetPricingOverride(map[string]any{"rule": map[string]any{"mode": PriceModePending}}).ExecX(ctx)
	check(0)
	if provider.purchases != 0 {
		t.Fatal("catalog submitted a purchase")
	}
}

func TestSocialDisplayQuantityBoundsAndUnverifiedBilling(t *testing.T) {
	n, _, provider, _, _ := nativeSocialEnv(t)
	for _, tc := range []struct {
		name                       string
		min, max, quantity, amount int64
	}{
		{"minimum above rate unit", 5000, 10000, 5000, 56},
		{"maximum below rate unit", 50, 100, 100, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := provider.schema
			s.Quantity.Min, s.Quantity.Max = &tc.min, &tc.max
			raw, _ := json.Marshal(s)
			var rules map[string]any
			_ = json.Unmarshal(raw, &rules)
			price, ok := socialDisplayPrice(n.conn, &productPricingRule{Mode: PriceModeChannel}, rules)
			if !ok || price.Quantity != tc.quantity || price.AmountCents != tc.amount {
				t.Fatal(price, ok)
			}
		})
	}
	s := provider.schema
	s.Billing.Verified = false
	raw, _ := json.Marshal(s)
	var rules map[string]any
	_ = json.Unmarshal(raw, &rules)
	if price, ok := socialDisplayPrice(n.conn, &productPricingRule{Mode: PriceModeChannel}, rules); ok {
		t.Fatal("unverified rate displayed", price)
	}
	conn := *n.conn
	conn.Driver = "zcard"
	conn.ExchangeRate = 1
	rules["billing"] = map[string]any{"mode": "quoted_total", "currency": "CNY", "verified": true}
	rules["display_price"] = catalogport.SocialDisplayPrice{AmountCents: 123, Quantity: 1000, Currency: "CNY"}
	price, ok := socialDisplayPrice(&conn, &productPricingRule{Mode: PriceModeChannel}, rules)
	if !ok || price.AmountCents != 125 {
		t.Fatal("supplier retail sample not repriced", price, ok)
	}
}
