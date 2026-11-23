package supply

import (
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
	"testing"
)

func TestCardTotalPricePreservesWholeQuoteAndPricingPolicy(t *testing.T) {
	v := &cardContext{conn: &ent.SupplyConnection{ExchangeRate: 1.25, PriceMarkupPercent: 20, AutoSyncPrice: true, PriceRoundingMode: supplyconnection.PriceRoundingModeNone}, product: &ent.Product{Price: 600}, mapping: &ent.SupplyMapping{PricingOverride: map[string]any{"last_synced_price": 600, "rule": map[string]any{"mode": "channel"}}}}
	amount, cost, e := cardTotalPrice(v, 1001, 0, 3)
	if e != nil || amount != 1502 || cost != 1251 {
		t.Fatalf("whole quote amount=%d cost=%d: %v", amount, cost, e)
	}
	v.conn.ExchangeRate = 1
	v.conn.PriceMarkupPercent = 10
	v.product.Price = 2
	v.mapping.PricingOverride["last_synced_price"] = 2
	amount, cost, e = cardTotalPrice(v, 1, 0, 1)
	if e != nil || amount != 2 || cost != 1 {
		t.Fatalf("positive markup lost to rounding: %d/%d %v", amount, cost, e)
	}
	v.conn.PriceMarkupPercent = 0
	v.conn.PriceMarkupAmount = 2
	v.product.Price = 602
	v.mapping.PricingOverride["last_synced_price"] = 602
	amount, _, e = cardTotalPrice(v, 1001, 0, 3)
	if e != nil || amount != 1007 {
		t.Fatalf("per-unit fixed markup changed: %d %v", amount, e)
	}
	// Operator-edited unit prices remain deliberately fixed; confirmed card
	// premiums are still added and never dropped by a fixed selling price.
	v.product.Price = 800
	amount, _, e = cardTotalPrice(v, 1251, 250, 3)
	if e != nil || amount != 2650 {
		t.Fatalf("fixed selling price/premium: %d %v", amount, e)
	}
}
