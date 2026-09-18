package adapter

import (
	"context"
	"encoding/json"
	"fmt"
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
	if mode := int(a.stockMode.Load()); mode > 0 {
		n, err := a.queryStockRoute(ctx, mode-1, code, fields)
		if !missingStockRoute(err) {
			return n, err
		}
	}
	// Probe once per adapter/batch; normal reads remain concurrent afterwards.
	a.stockProbe.Lock()
	defer a.stockProbe.Unlock()
	if mode := int(a.stockMode.Load()); mode > 0 {
		n, err := a.queryStockRoute(ctx, mode-1, code, fields)
		if !missingStockRoute(err) {
			return n, err
		}
	}
	for i := range acgStockRoutes {
		if len(fields) > 0 && i > 0 && (i < 3 || len(fields) != 1 || fields["race"] == "") {
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
	if mode > 0 && len(fields) > 0 && (mode < 3 || len(fields) != 1 || fields["race"] == "") {
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
	if err := json.Unmarshal(raw, &d); err != nil {
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
		ini, _ := parseAcgINI(cfg)
		if ini != nil && (len(ini.Sku) > 0 || (len(ini.Race) > 0 && fields["race"] == "")) {
			return -2, fmt.Errorf("旧版库存接口需要指定规格")
		}
	}
	return parseStock(d[route.field])
}
