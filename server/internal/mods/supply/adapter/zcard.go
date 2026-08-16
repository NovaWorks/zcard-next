package adapter

// zcard.go — 自家 Supply v2 协议（P2-03 服务端对偶，同一协议两种拓扑）。
// 端点与字段对齐 1.x ZCardDriver + 规划 §5.8；签名 4 头 HMAC（新口径含 query md5 段）。

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
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
	return a.t.do(ctx, method, path, query, a.signHeaders(method, path, rawQuery, raw), raw)
}

func (a *zCardAdapter) Ping(ctx context.Context) (*PingResult, error) {
	data, err := a.request(ctx, "POST", "/api/supply/ping", nil, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		OK       bool   `json:"ok"`
		Name     string `json:"name"`
		Balance  int64  `json:"balance"`
		Currency string `json:"currency"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.zcard: 解析 ping 响应失败: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("adapter.zcard: 上游 ping 返回 ok=false")
	}
	return &PingResult{SiteName: resp.Name, Balance: resp.Balance, Currency: resp.Currency}, nil
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
	if err := json.Unmarshal(data, &resp); err != nil {
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
	q := url.Values{}
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
		Items    []zCardProduct `json:"items"`
		Total    int            `json:"total"`
		PageSize int            `json:"page_size"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.zcard: 解析商品列表失败: %w", err)
	}
	pageSize = resp.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	out := &ProductList{Total: resp.Total, HasMore: page*pageSize < resp.Total}
	for _, p := range resp.Items {
		out.Items = append(out.Items, p.toProduct())
	}
	return out, nil
}

func (a *zCardAdapter) GetStock(ctx context.Context, productCode, _ string) (int32, error) {
	path := "/api/supply/products/" + url.PathEscape(productCode) + "/stock"
	data, err := a.request(ctx, "GET", path, nil, nil)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Stock int32 `json:"stock"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
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
	data, err := a.request(ctx, "POST", "/api/supply/orders", nil, body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		SupplyOrderID any    `json:"supply_order_id"`
		Amount        int64  `json:"amount"`
		Status        string `json:"status"`
		Fulfillment   struct {
			Status string   `json:"status"`
			Cards  []string `json:"cards"`
		} `json:"fulfillment"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.zcard: 解析下单响应失败: %w", err)
	}
	status := resp.Status
	if status == "" {
		status = resp.Fulfillment.Status
	}
	return &CreateOrderResult{
		UpstreamOrderID: idString(resp.SupplyOrderID),
		Status:          status,
		Amount:          resp.Amount,
		Cards:           resp.Fulfillment.Cards,
	}, nil
}

func (a *zCardAdapter) GetOrder(ctx context.Context, upstreamOrderID string) (*OrderDetail, error) {
	path := "/api/supply/orders/" + url.PathEscape(upstreamOrderID)
	data, err := a.request(ctx, "GET", path, nil, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		SupplyOrderID any    `json:"supply_order_id"`
		Status        string `json:"status"`
		Amount        int64  `json:"amount"`
		Fulfillment   struct {
			Status string   `json:"status"`
			Cards  []string `json:"cards"`
		} `json:"fulfillment"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.zcard: 解析订单详情失败: %w", err)
	}
	status := resp.Status
	if status == "" {
		status = resp.Fulfillment.Status
	}
	return &OrderDetail{
		UpstreamOrderID: upstreamOrderID,
		Status:          status,
		Amount:          resp.Amount,
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
	_ = json.Unmarshal(data, &resp)
	if !resp.OK {
		return fmt.Errorf("adapter.zcard: 上游退款返回 ok=false")
	}
	return nil
}

// zCardProduct 上游商品行（字段对齐 1.x mapProduct）。
type zCardProduct struct {
	ID           any    `json:"id"`
	Name         string `json:"name"`
	Price        int64  `json:"price"`
	FactoryPrice int64  `json:"factory_price"`
	CategoryID   any    `json:"category_id"`
	Description  string `json:"description"`
	Cover        string `json:"cover"`
	IsActive     bool   `json:"is_active"`
	Stock        int32  `json:"stock"`
}

func (p zCardProduct) toProduct() Product {
	return Product{
		ID:           idString(p.ID),
		Name:         p.Name,
		CategoryID:   idString(p.CategoryID),
		Price:        p.Price,
		FactoryPrice: p.FactoryPrice,
		Description:  p.Description,
		Cover:        p.Cover,
		IsActive:     p.IsActive,
		Stock:        p.Stock,
	}
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
