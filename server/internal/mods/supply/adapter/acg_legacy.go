package adapter

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Legacy Shared/Commodity::inventory computes factory_price for the signed
// account and category_factory for each race. It cannot price arbitrary [sku]
// combinations. Catalog price/user_price and category display prices are NEVER
// substitutes. Called only after the modern valuation route is proven absent.
func legacyAccountPrices(inv *acgInventory) (map[string]int64, error) {
	var meta struct {
		DeliveryWay *int            `json:"delivery_way"`
		DraftStatus *int            `json:"draft_status"`
		Config      *string         `json:"config"`
		Category    json.RawMessage `json:"is_category"`
		Price       json.RawMessage `json:"factory_price"`
	}
	if err := json.Unmarshal(inv.raw, &meta); err != nil {
		return nil, err
	}
	if meta.DeliveryWay == nil || *meta.DeliveryWay != 0 || meta.DraftStatus == nil || *meta.DraftStatus != 0 || meta.Config == nil {
		return nil, fmt.Errorf("无法确认旧版货源的发货方式或报价规格")
	}
	races, prices, err := legacyPriceSections(*meta.Config)
	if err != nil {
		return nil, err
	}
	category := string(meta.Category)
	if (len(races) == 0 && category != "false" && category != "0") || (len(races) > 0 && category != "true" && category != "1") {
		return nil, fmt.Errorf("旧版货源规格标记与账号报价不一致")
	}
	if len(races) == 0 {
		if len(prices) != 0 {
			return nil, fmt.Errorf("旧版货源规格报价不完整")
		}
		price, err := parseAccountPrice(meta.Price)
		if err != nil {
			return nil, err
		}
		return map[string]int64{"": price}, nil
	}
	if len(races) != len(prices) {
		return nil, fmt.Errorf("旧版货源规格账号报价不完整")
	}
	out := make(map[string]int64, len(races))
	for race := range races {
		value, ok := prices[race]
		if !ok {
			return nil, fmt.Errorf("旧版货源规格账号报价缺失")
		}
		raw, _ := json.Marshal(value)
		price, err := parseAccountPrice(raw)
		if err != nil {
			return nil, err
		}
		out[race] = price
	}
	return out, nil
}

// Strict for legacy reads: a malformed/unknown spec must not become a plain
// product. Unlike catalog previews, these values authorize prices and stock.
func legacyPriceSections(config string) (map[string]string, map[string]string, error) {
	races, prices := map[string]string{}, map[string]string{}
	section := ""
	categorySeen, factorySeen := false, false
	for _, line := range strings.Split(strings.ReplaceAll(config, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			categorySeen = categorySeen || section == "category"
			factorySeen = factorySeen || section == "category_factory"
			if section == "sku" {
				return nil, nil, fmt.Errorf("%w: 旧版货源无法确认组合规格的报价和库存", ErrNotSupported)
			}
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || section == "" {
			return nil, nil, fmt.Errorf("旧版货源规格配置无效")
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		var target map[string]string
		switch section {
		case "category":
			target = races
		case "category_factory":
			target = prices
		default:
			continue
		}
		if key == "" || value == "" || !specDelimOK(key) {
			return nil, nil, fmt.Errorf("旧版货源规格配置无效")
		}
		if _, exists := target[key]; exists {
			return nil, nil, fmt.Errorf("旧版货源规格重复")
		}
		target[key] = value
	}
	if (categorySeen && len(races) == 0) || (factorySeen && len(prices) == 0) {
		return nil, nil, fmt.Errorf("旧版货源规格配置为空")
	}
	return races, prices, nil
}
