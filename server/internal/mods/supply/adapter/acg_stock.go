package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Candidate parameters are protocol-specific. item is product-level only;
// inventory supports race but cannot filter arbitrary SKU dimensions.
var acgStockRoutes = []struct{ path, param, field string }{
	{"/shared/commodity/stock", "code", "stock"},
	{"/shared/commodity/item", "code", "stock"},
	{"/shared/commodity/item", "sharedCode", "stock"},
	{"/shared/commodity/inventory", "sharedCode", "count"},
}

var errLegacyItemWithoutStock = errors.New("旧版商品详情未提供库存字段")

func supportsAcgStockFields(mode int, fields map[string]string) bool {
	return len(fields) == 0 || mode == 0 || (mode == 3 && len(fields) == 1 && fields["race"] != "")
}

func (a *acgFakaAdapter) GetStock(ctx context.Context, code, sku string) (int32, error) {
	ctx = stockReadContext(ctx)
	fields := map[string]string{}
	if sku != "" {
		var err error
		fields, err = AcgSpecFormFields(sku)
		if err != nil {
			return -2, err
		}
	}
	if mode := int(a.stockMode.Load()); mode > 0 && supportsAcgStockFields(mode-1, fields) {
		n, err := a.queryStockRoute(ctx, mode-1, code, fields)
		if !missingStockRoute(err) && !errors.Is(err, errLegacyItemWithoutStock) {
			return n, err
		}
	}
	// Probe once per adapter/batch; normal reads remain concurrent afterwards.
	a.stockProbe.Lock()
	defer a.stockProbe.Unlock()
	if mode := int(a.stockMode.Load()); mode > 0 && supportsAcgStockFields(mode-1, fields) {
		n, err := a.queryStockRoute(ctx, mode-1, code, fields)
		if !missingStockRoute(err) && !errors.Is(err, errLegacyItemWithoutStock) {
			return n, err
		}
	}
	skipItemVariant := false
	for i := range acgStockRoutes {
		if i == 2 && skipItemVariant {
			continue
		}
		if !supportsAcgStockFields(i, fields) {
			continue
		}
		n, err := a.queryStockRoute(ctx, i, code, fields)
		if err == nil {
			a.stockMode.Store(int32(i + 1))
			return n, nil
		}
		if missingStockRoute(err) {
			continue
		}
		if errors.Is(err, errLegacyItemWithoutStock) {
			skipItemVariant = true
			continue
		}
		// Old skins recognize sharedCode instead of code. Only a missing-code error
		// can select that variant; product/auth/business errors are not fallback signals.
		if i == 1 && (strings.Contains(err.Error(), "CODE不能为空") || strings.Contains(err.Error(), "商品代码不能为空") || strings.Contains(err.Error(), "sharedCode不能为空")) {
			continue
		}
		return -2, err
	}
	return -2, fmt.Errorf("货源没有兼容当前商品规格的库存接口")
}

func (a *acgFakaAdapter) queryStockRoute(ctx context.Context, mode int, code string, fields map[string]string) (int32, error) {
	if !supportsAcgStockFields(mode, fields) {
		return -2, fmt.Errorf("旧版货源接口不支持当前规格的库存查询")
	}
	route := acgStockRoutes[mode]
	params := map[string]string{route.param: code}
	for k, v := range fields {
		params[k] = v
	}
	body, err := a.signedPost(ctx, route.path, params)
	if err != nil {
		return -2, err
	}
	raw, err := parseResp(body)
	if err != nil {
		return -2, err
	}
	var d map[string]json.RawMessage
	if (mode == 1 || mode == 2) && strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		d, err = legacyItemStock(raw, code)
		if err != nil {
			return -2, err
		}
	} else if err := json.Unmarshal(raw, &d); err != nil {
		return -2, fmt.Errorf("上游库存响应格式无效")
	}
	if mode == 3 {
		// inventory.count is meaningless for manual delivery; with no race the old
		// endpoint selects only the first race, so do not advertise it as total stock.
		var way *int
		if json.Unmarshal(d["delivery_way"], &way) != nil || way == nil || *way != 0 {
			return -2, fmt.Errorf("无法确认旧版库存接口的发货方式")
		}
		var cfg string
		if v := d["config"]; len(v) > 0 && string(v) != "null" && json.Unmarshal(v, &cfg) != nil {
			return -2, fmt.Errorf("无法确认旧版库存接口的规格")
		}
		races, _, err := legacyPriceSections(cfg)
		if err != nil {
			return -2, err
		}
		if category := strings.TrimSpace(string(d["is_category"])); (category == "true" || category == "1") && len(races) == 0 {
			return -2, fmt.Errorf("旧版库存规格配置缺失")
		}
		if race := fields["race"]; race != "" {
			if _, ok := races[race]; !ok {
				return -2, fmt.Errorf("旧版库存接口未返回请求的规格")
			}
		} else if len(races) > 0 {
			if len(races) > MaxAcgCombos {
				return -2, fmt.Errorf("旧版库存规格数量超限")
			}
			// Old inventory with no race returns the FIRST race only. Confirm each
			// race and sum all counts; never publish a partial aggregate on failure.
			names := make([]string, 0, len(races))
			for race := range races {
				names = append(names, race)
			}
			sort.Strings(names)
			var total int64
			unlimited := false
			for _, race := range names {
				n, err := a.queryStockRoute(ctx, 3, code, map[string]string{"race": race})
				if err != nil {
					return -2, err
				}
				if n == -1 {
					unlimited = true
				} else {
					total += int64(n)
				}
				if total > math.MaxInt32 {
					return -2, fmt.Errorf("旧版库存总数超限")
				}
			}
			if unlimited {
				return -1, nil
			}
			return int32(total), nil
		}
	}
	return parseStock(d[route.field])
}

// Some skins return a category tree from /item. Match exactly one requested
// product; missing stock may use inventory, but corrupt stock or a different
// product must not authorize a fallback or a zero-stock write.
func legacyItemStock(raw json.RawMessage, code string) (map[string]json.RawMessage, error) {
	var cats []struct {
		Children []map[string]json.RawMessage `json:"children"`
	}
	if err := json.Unmarshal(raw, &cats); err != nil {
		return nil, fmt.Errorf("旧版商品详情格式无效")
	}
	var match map[string]json.RawMessage
	for _, cat := range cats {
		for _, p := range cat.Children {
			var got string
			if json.Unmarshal(p["code"], &got) != nil || got != code {
				continue
			}
			if match != nil {
				return nil, fmt.Errorf("旧版商品详情返回重复商品")
			}
			match = p
		}
	}
	if match == nil {
		return nil, fmt.Errorf("旧版商品详情未返回请求的商品")
	}
	if stock := strings.TrimSpace(string(match["stock"])); stock == "" || stock == "null" {
		return nil, errLegacyItemWithoutStock
	}
	return match, nil
}
