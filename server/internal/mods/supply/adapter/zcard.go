package adapter

// zcard.go — 自家 Supply v2 协议（ 服务端对偶，同一协议两种拓扑）。
// 端点与字段对齐 1.x ZCardDriver + 规划 ；签名 4 头 HMAC（新口径含 query md5 段）。

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/cardpurchase"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/NovaWorks/zcard-next/server/internal/platform/purchaseform"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// zCardAdapter zcard 协议适配器。
type zCardAdapter struct {
	protocol string
	creds    Credentials
	t        *transport
}

func newZCard(baseURL string, creds Credentials, retryIntervals []int) (Adapter, error) {
	// Accept both the site root and the Supply API URL. Keep the stored URL
	// unchanged: encrypted credentials are bound to its exact bytes.
	baseURL = strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/api/supply")
	t, err := newTransport(baseURL, retryIntervals, slog.Default())
	if err != nil {
		return nil, err
	}
	return &zCardAdapter{protocol: "zcard", creds: creds, t: t}, nil
}

func (a *zCardAdapter) Protocol() string { return a.protocol }

// signHeaders 构造 4 头。rawQuery 与 body 均为实际发出字节。
func (a *zCardAdapter) signHeaders(method, path, rawQuery string, body []byte) map[string]string {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := fmt.Sprintf("zc_%d_%s", time.Now().UnixNano(), randSuffix(8))
	sig := ZCardSign(a.creds.APISecret, method, path, rawQuery, ts, nonce, body)
	return map[string]string{
		"X-Supply-Key":       a.creds.APIKey,
		"X-Supply-Timestamp": ts,
		"X-Supply-Nonce":     nonce,
		"X-Supply-Signature": sig,
		"Content-Type":       "application/json",
	}
}

// request JSON 请求（query 参与签名）。
func (a *zCardAdapter) request(ctx context.Context, method, path string, query url.Values, body any) ([]byte, error) {
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("adapter.zcard: 序列化请求失败: %w", err)
		}
	}
	rawQuery := ""
	if len(query) > 0 {
		rawQuery = query.Encode()
	}
	ctx = context.WithValue(ctx, requestSignerKey{}, requestSigner(func() map[string]string { return a.signHeaders(method, path, rawQuery, raw) }))
	return a.t.do(ctx, method, path, query, nil, raw)
}

func (a *zCardAdapter) Ping(ctx context.Context) (*PingResult, error) {
	data, err := a.request(ctx, "GET", "/api/supply/ping", nil, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		OK           bool                `json:"ok"`
		Name         string              `json:"name"`
		Balance      *supplyport.Integer `json:"balance"`
		Currency     string              `json:"currency"`
		Protocol     string              `json:"protocol"`
		Capabilities []string            `json:"capabilities"`
	}
	if err := decodeZCard(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.zcard: 解析 ping 响应失败: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("adapter.zcard: 上游 ping 返回 ok=false")
	}
	balance := int64(-1)
	if resp.Balance != nil {
		balance = int64(*resp.Balance)
	} else if resp.Currency != "" {
		// The server's JSON encoder omits a known zero balance.
		balance = 0
	}
	return &PingResult{SiteName: resp.Name, Balance: balance, Currency: resp.Currency, ProtocolVersion: resp.Protocol, Capabilities: resp.Capabilities}, nil
}

func (a *zCardAdapter) ListCategories(ctx context.Context) ([]Category, error) {
	data, err := a.request(ctx, "POST", "/api/supply/categories", nil, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Categories []struct {
			ID       any    `json:"id"`
			Name     string `json:"name"`
			ParentID any    `json:"parent_id"`
		} `json:"categories"`
	}
	if err := decodeZCard(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.zcard: 解析分类响应失败: %w", err)
	}
	out := make([]Category, 0, len(resp.Categories))
	for _, c := range resp.Categories {
		out = append(out, Category{
			ID:       idString(c.ID),
			Name:     c.Name,
			ParentID: idString(c.ParentID),
		})
	}
	return out, nil
}

func (a *zCardAdapter) ListProducts(ctx context.Context, page, pageSize int, includeInactive bool) (*ProductList, error) {
	q := smsCatalogQuery()
	q.Set("page", strconv.Itoa(page))
	if pageSize > 0 {
		q.Set("page_size", strconv.Itoa(pageSize))
	}
	if includeInactive {
		q.Set("include_inactive", "true")
	}
	data, err := a.request(ctx, "GET", "/api/supply/products", q, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items    []zCardProduct      `json:"items"`
		Total    *supplyport.Integer `json:"total"`
		HasMore  *bool               `json:"has_more"`
		PageSize int                 `json:"page_size"`
	}
	if err := decodeZCard(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.zcard: 解析商品列表失败: %w", err)
	}
	pageSize = resp.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	// 自家协议 include_inactive 恒生效（服务端 status=-1 全量；协议无回声字段）
	if page < 1 || pageSize > 1000 || len(resp.Items) > pageSize || resp.Total == nil || *resp.Total < 0 || resp.Items == nil {
		return nil, fmt.Errorf("adapter.zcard: invalid catalog page")
	}
	out := &ProductList{Total: int(*resp.Total), PageSize: pageSize, HasMore: int64(page)*int64(pageSize) < int64(*resp.Total), IncludesInactive: includeInactive}
	if resp.HasMore != nil {
		out.HasMore = *resp.HasMore
	}
	if out.HasMore && len(resp.Items) == 0 {
		return nil, fmt.Errorf("adapter.zcard: empty non-final page")
	}
	for _, p := range resp.Items {
		out.Items = append(out.Items, p.toProduct())
	}
	return out, nil
}

func (a *zCardAdapter) GetStock(ctx context.Context, productCode, sku string) (int32, error) {
	ctx = stockReadContext(ctx)
	path := "/api/supply/products/" + url.PathEscape(productCode) + "/stock"
	q := smsCatalogQuery()
	if sku != "" {
		q.Set("sku_id", sku)
	}
	data, err := a.request(ctx, "GET", path, q, nil)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Stock int32 `json:"stock"`
	}
	if err := decodeZCard(data, &resp); err != nil {
		return 0, fmt.Errorf("adapter.zcard: 解析库存失败: %w", err)
	}
	return resp.Stock, nil
}

func (a *zCardAdapter) CreateOrder(ctx context.Context, req CreateOrderReq) (*CreateOrderResult, error) {
	body := map[string]any{
		"product_id":          req.ProductCode,
		"quantity":            req.Quantity,
		"downstream_order_no": req.DownstreamOrderNo,
		"callback_url":        req.CallbackURL,
	}
	if req.CardID != "" && req.CardQuoteID == "" {
		return nil, ErrPurchaseFormInvalid
	}
	if req.CardQuoteID != "" {
		if req.CardMaxAmountCents <= 0 || !money.ValidCents(req.CardMaxAmountCents) || len(req.CardCurrency) != 3 || req.CardCurrency != strings.ToUpper(req.CardCurrency) {
			return nil, ErrPurchaseFormInvalid
		}
		body["required_capability"] = cardpurchase.Capability
		body["card_quote_id"] = req.CardQuoteID
		body["max_supply_amount_cents"] = req.CardMaxAmountCents
		body["currency"] = req.CardCurrency
		body["sku_id"] = req.UpstreamSKU
		body["manual_form_data"] = req.ManualFormData
	} else if req.UpstreamSKU != "" || len(req.ManualFormData) > 0 {
		body["required_capability"] = "card_purchase.v1"
		body["sku_id"] = req.UpstreamSKU
		body["manual_form_data"] = req.ManualFormData
	}
	data, err := a.request(ctx, "POST", "/api/supply/orders", nil, body)
	if err != nil {
		switch strings.ToLower(upstreamErrorCode(err)) {
		case "supply.form_invalid", "supply.sku_invalid", "supply.sku_required", "supply.capability_required":
			return nil, ErrPurchaseFormInvalid
		}
		return nil, err
	}
	var resp struct {
		ErrorCode     string             `json:"error_code"`
		Charged       bool               `json:"charged"`
		SupplyOrderID any                `json:"supply_order_id"`
		Amount        supplyport.Integer `json:"amount"`
		Status        string             `json:"status"`
		Fulfillment   struct {
			Status string   `json:"status"`
			Cards  []string `json:"cards"`
		} `json:"fulfillment"`
	}
	if err := decodeZCard(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.zcard: 解析下单响应失败: %w", err)
	}
	if req.CardQuoteID != "" && (resp.Status == "rejected" || resp.Status == "refunded") {
		// Only an owned receipt with a definitive zero net supplier charge can
		// authorize the existing permanent-failure refund path.
		id := idString(resp.SupplyOrderID)
		if receiptID, e := strconv.ParseUint(id, 10, 64); e != nil || receiptID == 0 {
			return nil, ErrPurchaseUncertain
		}
		proof := func(cause error) error {
			return &supplyport.CardPurchaseRejection{QuoteID: req.CardQuoteID, ReceiptID: id, Currency: req.CardCurrency, Status: resp.Status, ErrorCode: resp.ErrorCode, AmountCents: int64(resp.Amount), Charged: resp.Charged, Cause: cause}
		}
		if resp.Status == "refunded" {
			if !resp.Charged || int64(resp.Amount) != req.CardMaxAmountCents {
				return nil, ErrPurchaseUncertain
			}
			switch resp.ErrorCode {
			case "no_stock":
				return nil, proof(ErrNoStock)
			case "card_quote_changed":
				return nil, proof(ErrProductUnavailable)
			default:
				return nil, ErrPurchaseUncertain
			}
		}
		if resp.Charged || resp.Amount != 0 {
			return nil, ErrPurchaseUncertain
		}
		switch resp.ErrorCode {
		case "no_stock":
			return nil, proof(ErrNoStock)
		case "insufficient_balance":
			return nil, proof(ErrInsufficientBalance)
		case "card_quote_expired", "product_unavailable", "product_changed", "SUPPLY_RESTRICTED", "price_changed", "connection_unavailable", "connection_changed":
			return nil, proof(ErrProductUnavailable)
		default:
			return nil, ErrPurchaseUncertain
		}
	}
	if resp.Status == "rejected" {
		if resp.Charged {
			return nil, ErrPurchaseUncertain
		}
		switch resp.ErrorCode {
		case "no_stock":
			return nil, ErrNoStock
		case "product_unavailable":
			return nil, ErrProductUnavailable
		case "insufficient_balance":
			return nil, ErrInsufficientBalance
		default:
			return nil, ErrPurchaseUncertain
		}
	}
	status := resp.Status
	if status == "fulfilled" && resp.Fulfillment.Status == "delivered" {
		status = "delivered"
	}
	if status == "fulfilling" {
		status = "pending"
	}
	if status == "" {
		status = resp.Fulfillment.Status
	}
	return &CreateOrderResult{
		UpstreamOrderID: idString(resp.SupplyOrderID),
		Status:          status,
		Amount:          int64(resp.Amount),
		Cards:           resp.Fulfillment.Cards,
	}, nil
}

// ListOrders 上游订单列表（ 对账数据源；自家协议端点）。
func (a *zCardAdapter) ListOrders(ctx context.Context, start, end time.Time) ([]OrderDetail, error) {
	q := url.Values{}
	q.Set("start", strconv.FormatInt(start.Unix(), 10))
	q.Set("end", strconv.FormatInt(end.Unix(), 10))
	data, err := a.request(ctx, "GET", "/api/supply/orders", q, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Orders []struct {
			ID        any                `json:"id"`
			Amount    supplyport.Integer `json:"amount"`
			Status    string             `json:"status"`
			CreatedAt int64              `json:"created_at"`
		} `json:"orders"`
	}
	if err := decodeZCard(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.zcard: 解析订单列表失败: %w", err)
	}
	out := make([]OrderDetail, 0, len(resp.Orders))
	for _, o := range resp.Orders {
		out = append(out, OrderDetail{
			UpstreamOrderID: idString(o.ID),
			Status:          o.Status,
			Amount:          int64(o.Amount),
		})
	}
	return out, nil
}

func (a *zCardAdapter) GetOrder(ctx context.Context, upstreamOrderID string) (*OrderDetail, error) {
	path := "/api/supply/orders/" + url.PathEscape(upstreamOrderID)
	data, err := a.request(ctx, "GET", path, nil, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		SupplyOrderID any                `json:"supply_order_id"`
		Status        string             `json:"status"`
		Amount        supplyport.Integer `json:"amount"`
		Fulfillment   struct {
			Status string   `json:"status"`
			Cards  []string `json:"cards"`
		} `json:"fulfillment"`
	}
	if err := decodeZCard(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.zcard: 解析订单详情失败: %w", err)
	}
	status := resp.Status
	if status == "fulfilled" && resp.Fulfillment.Status == "delivered" {
		status = "delivered"
	}
	if status == "fulfilling" {
		status = "pending"
	}
	if status == "" {
		status = resp.Fulfillment.Status
	}
	return &OrderDetail{
		UpstreamOrderID: upstreamOrderID,
		Status:          status,
		Amount:          int64(resp.Amount),
		Cards:           resp.Fulfillment.Cards,
	}, nil
}

func (a *zCardAdapter) RefundOrder(ctx context.Context, upstreamOrderID string) error {
	path := "/api/supply/orders/" + url.PathEscape(upstreamOrderID) + "/refund"
	data, err := a.request(ctx, "POST", path, nil, nil)
	if err != nil {
		return err
	}
	var resp struct {
		OK bool `json:"ok"`
	}
	_ = decodeZCard(data, &resp)
	if !resp.OK {
		return fmt.Errorf("adapter.zcard: 上游退款返回 ok=false")
	}
	return nil
}

// zCardProduct 上游商品行（字段对齐 1.x mapProduct）。
type zCardProduct struct {
	CardRules    *cardpurchase.Rules `json:"card_purchase"`
	PurchaseForm json.RawMessage     `json:"purchase_form"`
	SKUs         []struct {
		ID       string             `json:"id"`
		Code     string             `json:"code"`
		Name     string             `json:"name"`
		Price    supplyport.Integer `json:"price"`
		Stock    int32              `json:"stock"`
		IsActive bool               `json:"is_active"`
	} `json:"skus"`
	ProductKind    string             `json:"product_kind"`
	DeliveryKind   string             `json:"delivery_kind"`
	SMSProduct     map[string]string  `json:"sms_product"`
	SocialSchema   map[string]any     `json:"social_schema"`
	ID             any                `json:"id"`
	Name           string             `json:"name"`
	Price          supplyport.Integer `json:"price"`
	FactoryPrice   supplyport.Integer `json:"factory_price"`
	CategoryID     any                `json:"category_id"`
	Description    string             `json:"description"`
	DescriptionSet bool               `json:"-"`
	Cover          string             `json:"cover"`
	IsActive       bool               `json:"is_active"`
	Stock          int32              `json:"stock"`
}

func (p zCardProduct) toProduct() Product {
	out := Product{
		CardRules:   p.CardRules,
		ProductKind: normalizedProductKind(p.ProductKind), DeliveryKind: normalizedDeliveryKind(p.DeliveryKind), SMSProduct: publicSMSProduct(p.SMSProduct), SocialSchema: p.SocialSchema,
		ID:             idString(p.ID),
		Name:           p.Name,
		CategoryID:     idString(p.CategoryID),
		Price:          int64(p.Price),
		FactoryPrice:   int64(p.Price),
		Description:    p.Description,
		DescriptionSet: p.DescriptionSet,
		Cover:          p.Cover,
		IsActive:       p.IsActive,
		Stock:          p.Stock,
	}
	if _, err := cardpurchase.Decode(p.CardRules); err != nil {
		out.IsActive = false
	}
	if p.SKUs != nil {
		out.UpstreamExtra = map[string]any{"sku_catalog_complete": true}
		out.SKUs = make([]SKU, 0, len(p.SKUs))
	}
	for _, v := range p.SKUs {
		if !v.IsActive {
			continue
		}
		name := v.Name
		if name == "" {
			name = v.Code
		}
		out.SKUs = append(out.SKUs, SKU{ID: v.ID, Code: v.ID, Name: name, Price: int64(v.Price), Stock: v.Stock, IsActive: v.IsActive, SpecValues: map[string]string{}})
	}
	if len(p.SKUs) > 0 && len(out.SKUs) == 0 {
		out.IsActive = false
	}
	fields, e := purchaseform.ParseSchema(p.PurchaseForm)
	if e != nil {
		out.IsActive = false
	} else {
		out.FormFields = fields
		// V2 metadata declares a complete purchase contract. An omitted empty
		// form therefore clears removed controls; legacy peers remain unknown.
		if fields == nil && p.CardRules != nil {
			out.FormFields = []purchaseform.Field{}
		}
	}
	return out
}

// idString 上游 id 可能是 number/string，统一字符串化（铁律：对外标识一律字符串）。
func idString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(x, 10)
	case json.Number:
		return x.String()
	default:
		return fmt.Sprintf("%v", x)
	}
}

// randSuffix 随机后缀（nonce 生成；crypto/rand 防并发碰撞）。
func randSuffix(n int) string {
	b := make([]byte, n)
	if _, err := crand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36) // 理论不可达
	}
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	var sb strings.Builder
	for _, c := range b {
		sb.WriteByte(chars[int(c)%len(chars)])
	}
	return sb.String()
}

func (p *zCardProduct) UnmarshalJSON(b []byte) error {
	type plain zCardProduct
	var v plain
	if err := decodeZCard(b, &v); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	*p = zCardProduct(v)
	raw, ok := fields["description"]
	p.DescriptionSet = ok && string(raw) != "null"
	return nil
}

func decodeZCard(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(v)
}
func smsCatalogQuery() url.Values {
	return url.Values{"capabilities": {supplyport.SMSCapability, supplyport.SMSProductCatalog, supplyport.SMSProductPurchase, supplyport.SocialCapability, "card_purchase.v1", cardpurchase.Capability}}
}
func normalizedDeliveryKind(v string) string {
	if v == "" {
		return "card"
	}
	return v
}
func publicSMSProduct(in map[string]string) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"country_id", "platform_id", "operator_id", "country_name", "platform_name", "operator_name", "provider_name"} {
		if v, ok := in[k]; ok {
			out[k] = v
		}
	}
	return out
}

func normalizedProductKind(v string) string {
	if v == "" {
		return "standard"
	}
	return v
}
