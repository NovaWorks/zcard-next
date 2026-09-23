package supply

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

// Import choices belong to the product; connection defaults are only defaults.
type productPricingRule struct {
	Mode    string  `json:"mode"`
	Percent float64 `json:"percent"`
	Amount  int64   `json:"amount"`
}

func (r productPricingRule) price(conn *ent.SupplyConnection, upstream int64) int64 {
	if r.Mode == PriceModeChannel {
		return ApplyPricing(upstream, conn.ExchangeRate, conn.PriceMarkupPercent, conn.PriceMarkupAmount, string(conn.PriceRoundingMode))
	}
	return ApplyPricingImport(upstream, conn.ExchangeRate, r.Percent, r.Amount, r.Mode, string(conn.PriceRoundingMode))
}

func readProductRule(override map[string]any) (*productPricingRule, error) {
	raw, ok := override["rule"]
	if !ok {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var r productPricingRule
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("商品定价规则无效: %w", err)
	}
	switch r.Mode {
	case PriceModeChannel, PriceModePercent, PriceModeFixed, PriceModeEqual, PriceModePending:
	default:
		return nil, fmt.Errorf("商品定价模式无效")
	}
	if err := validatePricing(1, r.Percent, r.Amount, RoundingNone); err != nil {
		return nil, err
	}
	return &r, nil
}

func copyPricingMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src)+1)
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// Called under the product transaction/lock. Missing historical rules deliberately
// preserve prices: a final sale price cannot reconstruct an old markup rule.
func (s *SyncService) productPrices(ctx context.Context, conn *ent.SupplyConnection, m *ent.SupplyMapping, p *adapter.Product, force bool) (int64, []catalogport.UpstreamSKUInput, map[string]any, error) {
	o := copyPricingMap(m.PricingOverride)
	rule, err := readProductRule(o)
	if err != nil {
		return -1, nil, nil, err
	}
	if rule == nil && m.LocalProductID == 0 {
		rule = &productPricingRule{Mode: PriceModeChannel}
		o["rule"] = rule
	}
	protected := !conn.AutoSyncPrice || rule == nil || rule.Mode == PriceModePending || !p.IsActive
	price := int64(-1)
	if !protected {
		price = rule.price(conn, p.Price)
		if price <= 0 {
			return -1, nil, nil, fmt.Errorf("商品 %s 报价无效，保留原价格", p.ID)
		}
	}
	if fixed, ok := o["price"]; ok && conn.AutoSyncPrice && p.IsActive {
		price = toInt64(fixed)
		if price <= 0 {
			return -1, nil, nil, fmt.Errorf("商品固定售价必须大于零")
		}
		protected = true // A fixed product price does not imply a SKU price.
	} else if price >= 0 && m.LocalProductID > 0 && !force {
		current, err := s.currentProductPrice(ctx, m.LocalProductID)
		if err != nil {
			return -1, nil, nil, err
		}
		last, ok := o["last_synced_price"]
		if !ok || current != toInt64(last) {
			price = -1
			protected = true
		}
	}
	if price >= 0 {
		o["last_synced_price"] = price
	}
	previous, _ := o["sku_prices"].(map[string]any)
	baselines := copyPricingMap(previous)
	current := map[string]int64{}
	if m.LocalProductID > 0 {
		rows, err := s.repo.entClient(ctx).ProductSku.Query().Where(productsku.ProductID(m.LocalProductID)).All(ctx)
		if err != nil {
			return -1, nil, nil, err
		}
		for _, sk := range rows {
			if sk.UpstreamSkuID != "" {
				current[sk.UpstreamSkuID] = sk.Price
			}
		}
	}
	var skus []catalogport.UpstreamSKUInput
	for _, sk := range p.SKUs {
		value := int64(-1)
		if !protected {
			value = rule.price(conn, sk.Price)
			if value <= 0 {
				return -1, nil, nil, fmt.Errorf("商品 %s 规格报价无效，保留原价格", p.ID)
			}
			if local, exists := current[sk.Code]; exists && !force {
				last, known := previous[sk.Code]
				if !known || local != toInt64(last) {
					value = -1
				}
			}
		}
		if value >= 0 {
			baselines[sk.Code] = value
		}
		skus = append(skus, catalogport.UpstreamSKUInput{Code: sk.Code, Name: sk.Name, SpecValues: sk.SpecValues, PriceCents: value})
	}
	o["sku_prices"] = baselines
	return price, skus, o, nil
}

func (s *SyncService) lockProductPricing(ctx context.Context, m *ent.SupplyMapping) error {
	if m == nil || m.LocalProductID == 0 {
		return nil
	}
	c := s.repo.entClient(ctx)
	if err := c.Product.UpdateOneID(m.LocalProductID).AddSort(0).Exec(ctx); err != nil {
		// Archived/missing mappings are handled by the catalog writer; do not recreate here.
		if ent.IsNotFound(err) {
			return nil
		}
		return err
	}
	return c.ProductSku.Update().Where(productsku.ProductID(m.LocalProductID)).AddPrice(0).Exec(ctx)
}

func accountCost(conn *ent.SupplyConnection, p *adapter.Product) int64 {
	if p.FactoryPrice <= 0 {
		return -1
	}
	cost := ApplyPricing(p.FactoryPrice, conn.ExchangeRate, 0, 0, RoundingNone)
	if cost <= 0 {
		return -1
	}
	return cost
}

// Serialize pricing writes with configuration changes; never commit a quote made
// for a different account or with stale connection pricing.
func (s *SyncService) checkPricingConnection(ctx context.Context, snapshot *ent.SupplyConnection) error {
	c := s.repo.entClient(ctx)
	if err := c.SupplyConnection.UpdateOneID(snapshot.ID).AddRetryMax(0).Exec(ctx); err != nil {
		return err
	}
	current, err := c.SupplyConnection.Get(ctx, snapshot.ID)
	if err != nil {
		return err
	}
	if previewIdentity(current) != previewIdentity(snapshot) || current.ExchangeRate != snapshot.ExchangeRate || current.PriceMarkupPercent != snapshot.PriceMarkupPercent || current.PriceMarkupAmount != snapshot.PriceMarkupAmount || current.PriceRoundingMode != snapshot.PriceRoundingMode || current.AutoSyncPrice != snapshot.AutoSyncPrice {
		return fmt.Errorf("货源账号或定价配置已变化，本次未改价，请重试")
	}
	return nil
}
