package adapter

// MCY OpenApi: the client contract is documented by acg-faka's mcyRequest
// and Shared type=1. Unlike acg_faka, credentials live in headers, not the form.
import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/platform/mcysign"
	"github.com/NovaWorks/zcard-next/server/internal/platform/purchaseform"
)

type mcyAdapter struct {
	creds Credentials
	t     *transport
}

func newMCY(baseURL string, creds Credentials, retryIntervals []int) (Adapter, error) {
	if strings.TrimSpace(creds.AppID) == "" || strings.TrimSpace(creds.AppKey) == "" {
		return nil, ErrCredentialsInvalid
	}
	t, err := newTransport(baseURL, retryIntervals, slog.Default())
	if err != nil {
		return nil, err
	}
	return &mcyAdapter{creds: creds, t: t}, nil
}

func (a *mcyAdapter) Protocol() string { return "mcy_shop" }

func (a *mcyAdapter) request(ctx context.Context, endpoint string, params map[string]string) (json.RawMessage, error) {
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	body, err := a.t.do(ctx, "POST", "/plugin/open-api/"+endpoint, nil, map[string]string{
		"Api-Id": a.creds.AppID, "Api-Signature": mcysign.Sign(params, a.creds.AppKey),
		"Content-Type": "application/x-www-form-urlencoded",
	}, []byte(form.Encode()))
	if err != nil {
		return nil, err
	}
	var response struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Code == nil {
		return nil, fmt.Errorf("adapter.mcy: 无效的接口响应")
	}
	if *response.Code != 200 {
		status := 400
		if *response.Code == 401 || *response.Code == 403 {
			status = *response.Code
		}
		// Upstream messages can echo submitted fields; keep them out of logs.
		return nil, &httpError{Status: status, Code: "mcy_rejected", Message: "萌次元拒绝请求，请检查账号权限、商品状态和购买参数"}
	}
	return response.Data, nil
}

func (a *mcyAdapter) Ping(ctx context.Context) (*PingResult, error) {
	raw, err := a.request(ctx, "connect", nil)
	if err != nil {
		return nil, err
	}
	var v struct {
		Username string          `json:"username"`
		Balance  json.RawMessage `json:"balance"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Username == "" {
		return nil, fmt.Errorf("adapter.mcy: 无效的连接响应")
	}
	balance, err := mcyMoney(v.Balance)
	if err != nil {
		return nil, err
	}
	return &PingResult{SiteName: v.Username, Balance: balance}, nil // MCY does not report a currency code.
}

type mcyItem struct {
	ID         json.Number     `json:"id"`
	Name       string          `json:"name"`
	Introduce  *string         `json:"introduce"`
	PictureURL string          `json:"picture_url"`
	Widget     json.RawMessage `json:"widget"`
	Category   struct {
		ID   json.Number `json:"id"`
		Name string      `json:"name"`
	} `json:"category"`
	SKUs []struct {
		ID    json.Number     `json:"id"`
		Name  string          `json:"name"`
		Price json.RawMessage `json:"stock_price"`
		Stock json.RawMessage `json:"stock"`
	} `json:"sku"`
}

func (a *mcyAdapter) catalog(ctx context.Context) ([]mcyItem, error) {
	raw, err := a.request(ctx, "items", nil)
	if err != nil {
		return nil, err
	}
	var items []mcyItem
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return nil, fmt.Errorf("adapter.mcy: 无效的商品目录")
	}
	return items, nil
}

func (a *mcyAdapter) ListCategories(ctx context.Context) ([]Category, error) {
	items, err := a.catalog(ctx)
	if err != nil {
		return nil, err
	}
	return mcyCategories(items), nil
}

func mcyCategories(items []mcyItem) []Category {
	out := make([]Category, 0)
	seen := map[string]bool{}
	for _, item := range items {
		id := item.Category.ID.String()
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, Category{ID: id, Name: item.Category.Name})
		}
	}
	return out
}

// MCY returns the whole visible catalog. It cannot advertise inactive/deleted
// products; missing products must never be interpreted as confirmed deletion.
func (a *mcyAdapter) ListProducts(ctx context.Context, page, _ int, _ bool) (*ProductList, error) {
	if page > 1 {
		return &ProductList{Items: []Product{}}, nil
	}
	items, err := a.catalog(ctx)
	if err != nil {
		return nil, err
	}
	out := &ProductList{Items: make([]Product, 0, len(items)), Categories: mcyCategories(items), Total: len(items), PageSize: len(items)}
	for _, item := range items {
		p, err := item.product()
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, *p)
	}
	return out, nil
}

func (a *mcyAdapter) PreviewProducts(ctx context.Context) (*ProductList, error) {
	return a.ListProducts(ctx, 1, 0, false)
}

func (a *mcyAdapter) item(ctx context.Context, code string) (*Product, error) {
	if !mcyNumericID(code) {
		return nil, ErrProductUnavailable
	}
	raw, err := a.request(ctx, "item", map[string]string{"id": code})
	if err != nil {
		return nil, err
	}
	var item mcyItem
	if err := json.Unmarshal(raw, &item); err != nil || item.ID.String() != code {
		return nil, fmt.Errorf("adapter.mcy: 商品详情与请求不符")
	}
	return item.product()
}

func (a *mcyAdapter) ResolveImportProducts(ctx context.Context, codes []string) (*ProductList, error) {
	out := &ProductList{Items: make([]Product, 0, len(codes)), Total: len(codes)}
	for _, code := range codes {
		p, err := a.item(ctx, code)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, *p)
	}
	return out, nil
}

func (a *mcyAdapter) QuoteProduct(ctx context.Context, p *Product) (*Product, error) {
	if len(p.SKUs) == 0 {
		return nil, ErrProductUnavailable
	}
	out := *p
	out.SKUs = append([]SKU(nil), p.SKUs...)
	out.Price = 0
	for i := range out.SKUs {
		raw, err := a.request(ctx, "amount", map[string]string{"sku_id": out.SKUs[i].ID, "quantity": "1"})
		if err != nil {
			return nil, err
		}
		var q struct {
			Amount json.RawMessage `json:"amount"`
		}
		if err := json.Unmarshal(raw, &q); err != nil {
			return nil, err
		}
		price, err := parseAccountPrice(q.Amount)
		if err != nil {
			return nil, err
		}
		out.SKUs[i].Price = price
		if out.Price == 0 || price < out.Price {
			out.Price = price
		}
	}
	out.FactoryPrice = out.Price
	return &out, nil
}

func (v mcyItem) product() (*Product, error) {
	if !mcyNumericID(v.ID.String()) || v.Name == "" || len(v.SKUs) == 0 {
		return nil, fmt.Errorf("adapter.mcy: 商品或规格缺失")
	}
	p := &Product{ID: v.ID.String(), Name: v.Name, CategoryID: v.Category.ID.String(), Cover: v.PictureURL,
		IsActive: true, FactoryPrice: -1, Stock: 0, DescriptionSet: v.Introduce != nil}
	if v.Introduce != nil {
		p.Description = *v.Introduce
	}
	seen := map[string]bool{}
	for _, sku := range v.SKUs {
		id := sku.ID.String()
		if !mcyNumericID(id) || seen[id] {
			return nil, fmt.Errorf("adapter.mcy: 商品规格标识无效或重复")
		}
		seen[id] = true
		price, err := parseAccountPrice(sku.Price)
		if err != nil {
			return nil, err
		}
		stock := mcyStock(sku.Stock)
		p.SKUs = append(p.SKUs, SKU{ID: id, Code: id, Name: sku.Name, Price: price, Stock: stock, IsActive: true})
		if p.Price == 0 || price < p.Price {
			p.Price = price
		}
		if stock < 0 || p.Stock < 0 {
			p.Stock = -2
		} else {
			p.Stock = int32(min(int64(p.Stock)+int64(stock), math.MaxInt32))
		}
	}
	var err error
	p.FormFields, err = mcyForm(v.Widget)
	return p, err
}

func mcyNumericID(v string) bool {
	id, err := strconv.ParseUint(v, 10, 64)
	return err == nil && id > 0 && strconv.FormatUint(id, 10) == v
}

// Text stock such as “大量” does not prove unlimited inventory.
func mcyStock(raw json.RawMessage) int32 {
	value := strings.Trim(string(raw), `"`)
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil || n < 0 {
		return -2
	}
	return int32(n)
}

func mcyMoney(raw json.RawMessage) (int64, error) {
	value := strings.Trim(string(raw), `"`)
	if value == "0" || value == "0.00" || value == "0.0" {
		return 0, nil
	}
	return parseAccountPrice(raw)
}

func mcyForm(raw json.RawMessage) ([]purchaseform.Field, error) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return nil, nil
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		raw = []byte(value)
	}
	var widgets []struct{ Name, Type, Title, Placeholder, Regex, Data string }
	if err := json.Unmarshal(raw, &widgets); err != nil {
		return nil, fmt.Errorf("adapter.mcy: 无效的购买控件")
	}
	fields := make([]map[string]any, 0, len(widgets))
	for _, w := range widgets {
		if w.Name == "sku_id" || w.Name == "quantity" || w.Name == "trade_no" || w.Name == "sign" {
			return nil, fmt.Errorf("%w: 购买控件与协议字段冲突", ErrNotSupported)
		}
		typ := w.Type
		if typ == "checkbox" {
			return nil, fmt.Errorf("%w: 暂不支持萌次元多选购买控件", ErrNotSupported)
		}
		if typ == "input" {
			typ = "text"
		}
		field := map[string]any{"key": w.Name, "type": typ, "label": w.Title, "placeholder": w.Placeholder, "regex": w.Regex, "required": true}
		if typ == "select" || typ == "radio" || typ == "checkbox" {
			field["options"] = strings.Split(strings.ReplaceAll(w.Data, "\r\n", "\n"), "\n")
		}
		fields = append(fields, field)
	}
	schema, _ := json.Marshal(map[string]any{"fields": fields})
	return purchaseform.ParseSchema(schema)
}

func (a *mcyAdapter) GetStock(ctx context.Context, productCode, skuCode string) (int32, error) {
	ctx = stockReadContext(ctx)
	p, err := a.item(ctx, productCode)
	if err != nil {
		return -2, err
	}
	if skuCode == "" {
		var total int64
		for _, sku := range p.SKUs {
			n, err := a.skuStock(ctx, sku.ID)
			if err != nil || n < 0 {
				return -2, err
			}
			total += int64(n)
		}
		return int32(min(total, math.MaxInt32)), nil
	}
	found := false
	for _, sku := range p.SKUs {
		found = found || sku.ID == skuCode
	}
	if !found {
		return 0, ErrProductUnavailable
	}
	return a.skuStock(ctx, skuCode)
}

func (a *mcyAdapter) skuStock(ctx context.Context, skuCode string) (int32, error) {
	raw, err := a.request(ctx, "sku/stock", map[string]string{"sku_id": skuCode})
	if err != nil {
		return -2, err
	}
	var v struct {
		Stock json.RawMessage `json:"stock"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return -2, err
	}
	return mcyStock(v.Stock), nil
}

func (a *mcyAdapter) CreateOrder(ctx context.Context, req CreateOrderReq) (*CreateOrderResult, error) {
	if req.Quantity < 1 || req.DownstreamOrderNo == "" {
		return nil, ErrPurchaseFormInvalid
	}
	p, err := a.item(ctx, req.ProductCode)
	if err != nil {
		return nil, err
	}
	skuID := req.UpstreamSKU
	if skuID == "" && len(p.SKUs) == 1 {
		skuID = p.SKUs[0].ID
	}
	found := false
	for _, sku := range p.SKUs {
		found = found || sku.ID == skuID
	}
	if !found {
		return nil, ErrProductUnavailable
	}
	tradeNo := md5Hex([]byte(req.DownstreamOrderNo))[:24]
	params := map[string]string{"sku_id": skuID, "quantity": strconv.Itoa(req.Quantity), "trade_no": tradeNo}
	for key, value := range req.ManualFormData {
		if _, reserved := params[key]; reserved || key == "sign" || strings.ContainsAny(key, "[]") {
			return nil, ErrPurchaseFormInvalid
		}
		switch value.(type) {
		case string, bool, float64, int, int64, json.Number:
			params[key] = fmt.Sprint(value)
		default:
			return nil, ErrPurchaseFormInvalid
		}
	}
	stateRaw, err := a.request(ctx, "sku/state", map[string]string{"sku_id": skuID, "quantity": strconv.Itoa(req.Quantity)})
	if err != nil {
		return nil, err
	}
	var state struct {
		State *bool `json:"state"`
	}
	if err := json.Unmarshal(stateRaw, &state); err != nil || state.State == nil {
		return nil, fmt.Errorf("adapter.mcy: 无效的库存预检响应")
	}
	if !*state.State {
		return nil, ErrNoStock
	}
	quote, err := a.request(ctx, "amount", map[string]string{"sku_id": skuID, "quantity": strconv.Itoa(req.Quantity)})
	if err != nil {
		return nil, err
	}
	var q struct {
		Amount json.RawMessage `json:"amount"`
	}
	if err := json.Unmarshal(quote, &q); err != nil {
		return nil, err
	}
	amount, err := parseAccountPrice(q.Amount)
	if err != nil {
		return nil, err
	}
	// No independent query contract is published. A lost or malformed purchase
	// receipt must stop automatic procurement rather than risk another charge.
	raw, err := a.request(withoutRetries(ctx), "trade", params)
	if err != nil {
		return nil, fmt.Errorf("%w: 萌次元采购结果未确认，请核对原订单", ErrPurchaseUncertain)
	}
	var v struct {
		Contents string `json:"contents"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || strings.TrimSpace(v.Contents) == "" {
		return nil, fmt.Errorf("%w: 萌次元未返回交付内容，请核对原订单", ErrPurchaseUncertain)
	}
	for _, placeholder := range []string{"此商品没有发货信息或正在发货中", "库存不足，请申请售后", "发货失败，请直接申请售后", "发货插件未启用，请申请售后"} {
		if strings.HasPrefix(strings.TrimSpace(v.Contents), placeholder) {
			return nil, fmt.Errorf("%w: 萌次元发货未完成，请核对原订单", ErrPurchaseUncertain)
		}
	}
	cards := []string{v.Contents}
	if req.Quantity > 1 {
		cards = splitCards(v.Contents)
		if len(cards) != req.Quantity {
			return nil, fmt.Errorf("%w: 萌次元交付数量不符，请核对原订单", ErrPurchaseUncertain)
		}
	}
	return &CreateOrderResult{UpstreamOrderID: tradeNo, Status: "delivered", Amount: amount, Cards: cards}, nil
}

func (a *mcyAdapter) GetOrder(context.Context, string) (*OrderDetail, error) {
	return nil, ErrNotSupported
}

func (a *mcyAdapter) RefundOrder(context.Context, string) error { return ErrNotSupported }
