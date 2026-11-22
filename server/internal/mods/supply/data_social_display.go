package supply

import (
	"context"
	"encoding/json"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/shopspring/decimal"
)

// DisplaySocialPrices uses the imported catalog snapshot and current retail rules.
// No provider request, quote creation or purchase occurs on a public catalog read.
func (g *Gateway) DisplaySocialPrices(ctx context.Context, products []catalogport.Product) map[uint64]catalogport.SocialDisplayPrice {
	out := map[uint64]catalogport.SocialDisplayPrice{}
	var ids, sources []uint64
	for _, p := range products {
		if p.DeliveryKind == supplyport.SocialDelivery && p.UpstreamSourceID > 0 {
			ids = append(ids, p.ID)
			sources = append(sources, p.UpstreamSourceID)
		}
	}
	if len(ids) == 0 {
		return out
	}
	c := g.repo.entClient(ctx)
	connections, err := c.SupplyConnection.Query().Where(supplyconnection.IDIn(sources...)).All(ctx)
	if err != nil {
		return out
	}
	mappings, err := c.SupplyMapping.Query().Where(supplymapping.LocalProductIDIn(ids...), supplymapping.ConnectionIDIn(sources...), supplymapping.UpstreamSku("")).All(ctx)
	if err != nil {
		return out
	}
	bySource := map[uint64]*ent.SupplyConnection{}
	byProduct := map[uint64]*ent.SupplyMapping{}
	for _, conn := range connections {
		bySource[conn.ID] = conn
	}
	for _, m := range mappings {
		byProduct[m.LocalProductID] = m
	}
	for _, p := range products {
		conn, m := bySource[p.UpstreamSourceID], byProduct[p.ID]
		if conn == nil || m == nil || m.ConnectionID != p.UpstreamSourceID || m.UpstreamProduct != p.UpstreamProductCode || string(conn.Status) != "active" || !supplyport.SupportsSocialRetail(conn.Driver, conn.ExchangeRate) {
			continue
		}
		rule, e := readProductRule(m.PricingOverride)
		if e != nil || rule == nil || rule.Mode == PriceModePending {
			continue
		}
		if _, fixed := m.PricingOverride["price"]; fixed {
			continue
		}
		if price, ok := socialDisplayPrice(conn, rule, p.SocialSchema); ok {
			out[p.ID] = price
		}
	}
	return out
}

func socialDisplayPrice(conn *ent.SupplyConnection, rule *productPricingRule, raw map[string]any) (catalogport.SocialDisplayPrice, bool) {
	var schema supplyport.SocialSchema
	b, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(b, &schema) != nil || schema.Version != 1 || !schema.Purchasable || !schema.Billing.Verified {
		return catalogport.SocialDisplayPrice{}, false
	}
	var cost, quantity int64
	if conn.Driver == "csn19" {
		if schema.Billing.Mode != "quantity" || (schema.Quantity.Mode != "explicit" && schema.Quantity.Mode != "derived_lines") {
			return catalogport.SocialDisplayPrice{}, false
		}
		quantity = max(int64(1), schema.Billing.RateUnit)
		if schema.Quantity.Min != nil {
			quantity = max(quantity, *schema.Quantity.Min)
		}
		step, base := int64(1), int64(0)
		if schema.Quantity.Step != nil {
			step = *schema.Quantity.Step
		}
		if schema.Quantity.StepBase != nil {
			base = *schema.Quantity.StepBase
		}
		if step < 1 || step > money.MaxCents || quantity > money.MaxCents || base < 0 || base > quantity {
			return catalogport.SocialDisplayPrice{}, false
		}
		quantity = base + ((quantity-base+step-1)/step)*step
		if schema.Quantity.Max != nil && quantity > *schema.Quantity.Max {
			if *schema.Quantity.Max < base {
				return catalogport.SocialDisplayPrice{}, false
			}
			quantity = base + ((*schema.Quantity.Max-base)/step)*step
		}
		if quantity < 1 || (schema.Quantity.Min != nil && quantity < *schema.Quantity.Min) {
			return catalogport.SocialDisplayPrice{}, false
		}
		exact, e := money.ExactServiceAmount(schema.Billing.Rate, "quantity", &quantity, schema.Billing.RateUnit, 1)
		if e != nil {
			return catalogport.SocialDisplayPrice{}, false
		}
		d, e := decimal.NewFromString(exact)
		if e != nil {
			return catalogport.SocialDisplayPrice{}, false
		}
		converted := d.Mul(decimal.NewFromFloat(conn.ExchangeRate)).Shift(2).Ceil()
		if !converted.IsPositive() || converted.GreaterThan(decimal.NewFromInt(money.MaxCents)) {
			return catalogport.SocialDisplayPrice{}, false
		}
		cost = converted.IntPart()
	} else {
		// A ZCard supplier exposes only its own retail sample, never its provider cost.
		var display catalogport.SocialDisplayPrice
		value, e := json.Marshal(raw["display_price"])
		if e != nil || json.Unmarshal(value, &display) != nil || display.Currency != "CNY" || display.AmountCents <= 0 || display.AmountCents > money.MaxCents || display.Quantity <= 0 {
			return catalogport.SocialDisplayPrice{}, false
		}
		cost, quantity = display.AmountCents, display.Quantity
	}
	pricing := *conn
	if conn.Driver == "csn19" {
		pricing.ExchangeRate = 1
	}
	amount := rule.price(&pricing, cost)
	if amount <= 0 || amount < cost || amount > money.MaxCents {
		return catalogport.SocialDisplayPrice{}, false
	}
	return catalogport.SocialDisplayPrice{AmountCents: amount, Quantity: quantity, Currency: "CNY"}, true
}
