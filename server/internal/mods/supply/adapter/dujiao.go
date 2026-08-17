package adapter

// dujiao.go — 独角数卡 next 协议（友商 internal/upstream 协议知识迁移）。
// 3 头 HMAC（Dujiao-Next-Api-Key/-Timestamp/-Signature）；分页 50/页；
// includes_inactive 回声字段防旧版误判（友商经验）。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// dujiaoAdapter dujiao_next 协议适配器。
type dujiaoAdapter struct {
	protocol string
	creds    Credentials
	t        *transport
}

func newDujiaoNext(baseURL string, creds Credentials, retryIntervals []int) (Adapter, error) {
	t, err := newTransport(baseURL, retryIntervals, slog.Default())
	if err != nil {
		return nil, err
	}
	return &dujiaoAdapter{protocol: "dujiao_next", creds: creds, t: t}, nil
}

func (a *dujiaoAdapter) Protocol() string { return a.protocol }

// signHeaders 3 头（签名串不含 nonce）。
func (a *dujiaoAdapter) signHeaders(method, path, rawQuery string, body []byte) map[string]string {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := DujiaoSign(a.creds.APISecret, method, path, ts, body)
	return map[string]string{
		"Dujiao-Next-Api-Key":       a.creds.APIKey,
		"Dujiao-Next-Timestamp":     ts,
		"Dujiao-Next-Signature":     sig,
		"Content-Type":              "application/json",
	}
}

func (a *dujiaoAdapter) request(ctx context.Context, method, path string, query url.Values, body any) ([]byte, error) {
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("adapter.dujiao: 序列化请求失败: %w", err)
		}
	}
	rawQuery := ""
	if len(query) > 0 {
		rawQuery = query.Encode()
	}
	return a.t.do(ctx, method, path, query, a.signHeaders(method, path, rawQuery, raw), raw)
}

// okResp 上游统一包裹 {ok, ...}（错误走 {error_code, error_message}，见 httpError）。
type okResp struct {
	OK bool `json:"ok"`
}

func (a *dujiaoAdapter) Ping(ctx context.Context) (*PingResult, error) {
	data, err := a.request(ctx, "POST", "/api/v1/upstream/ping", nil, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		OK              bool   `json:"ok"`
		SiteName        string `json:"site_name"`
		ProtocolVersion string `json:"protocol_version"`
		Balance         string `json:"balance"`
		Currency        string `json:"currency"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.dujiao: 解析 ping 响应失败: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("adapter.dujiao: 上游 ping 返回 ok=false")
	}
	return &PingResult{
		SiteName:        resp.SiteName,
		ProtocolVersion: resp.ProtocolVersion,
		Balance:         parseYuanToCents(resp.Balance), // 上游金额字符串为元
		Currency:        resp.Currency,
	}, nil
}

func (a *dujiaoAdapter) ListCategories(ctx context.Context) ([]Category, error) {
	data, err := a.request(ctx, "GET", "/api/v1/upstream/categories", nil, nil)
	if err != nil {
		// 旧版上游不支持分类 API（404）→ 返回空列表不报错（1.x 同款降级）
		if he, ok := err.(*httpError); ok && he.Status == 404 {
			return []Category{}, nil
		}
		return nil, err
	}
	var resp struct {
		OK         bool `json:"ok"`
		Categories []struct {
			ID       any    `json:"id"`
			Name     string `json:"name"`
			ParentID any    `json:"parent_id"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.dujiao: 解析分类响应失败: %w", err)
	}
	out := make([]Category, 0, len(resp.Categories))
	for _, c := range resp.Categories {
		out = append(out, Category{ID: idString(c.ID), Name: c.Name, ParentID: idString(c.ParentID)})
	}
	return out, nil
}

func (a *dujiaoAdapter) ListProducts(ctx context.Context, page, pageSize int, includeInactive bool) (*ProductList, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(pageSize))
	if includeInactive {
		q.Set("include_inactive", "true")
	}
	data, err := a.request(ctx, "GET", "/api/v1/upstream/products", q, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		OK               bool            `json:"ok"`
		Total            int             `json:"total"`
		Items            []dujiaoProduct `json:"items"`
		IncludesInactive bool            `json:"includes_inactive"` // 回声字段：旧版不识别参数 → false
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.dujiao: 解析商品列表失败: %w", err)
	}
	out := &ProductList{
		Total: resp.Total,
		HasMore: page*pageSize < resp.Total,
	}
	for _, p := range resp.Items {
		out.Items = append(out.Items, p.toProduct())
	}
	return out, nil
}

func (a *dujiaoAdapter) GetStock(ctx context.Context, productCode, _ string) (int32, error) {
	// dujiao 无独立库存端点：走商品详情（stockQuantity，-1=无限）
	path := "/api/v1/upstream/products/" + url.PathEscape(productCode)
	data, err := a.request(ctx, "GET", path, nil, nil)
	if err != nil {
		switch upstreamErrorCode(err) {
		case "product_deleted", "product_not_found":
			return 0, ErrProductDeleted
		case "product_unavailable":
			return 0, ErrProductUnavailable
		}
		return 0, err
	}
	var resp struct {
		OK      bool `json:"ok"`
		Product struct {
			StockQuantity int32 `json:"stock_quantity"`
		} `json:"product"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, fmt.Errorf("adapter.dujiao: 解析商品详情失败: %w", err)
	}
	return resp.Product.StockQuantity, nil
}

func (a *dujiaoAdapter) CreateOrder(ctx context.Context, req CreateOrderReq) (*CreateOrderResult, error) {
	body := map[string]any{
		"sku_id":              req.ProductCode,
		"quantity":            req.Quantity,
		"downstream_order_no": req.DownstreamOrderNo,
		"trace_id":            req.TraceID,
		"callback_url":        req.CallbackURL,
	}
	data, err := a.request(ctx, "POST", "/api/v1/upstream/orders", nil, body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		OK           bool   `json:"ok"`
		OrderID      any    `json:"order_id"`
		OrderNo      string `json:"order_no"`
		Status       string `json:"status"`
		Amount       string `json:"amount"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.dujiao: 解析下单响应失败: %w", err)
	}
	if !resp.OK {
		// 余额不足/无映射等业务拒绝 → 归一化哨兵（T2 状态机判永久错误）
		switch resp.ErrorCode {
		case "insufficient_balance", "balance_not_enough":
			return nil, ErrInsufficientBalance
		case "product_unavailable":
			return nil, ErrProductUnavailable
		case "no_stock", "out_of_stock":
			return nil, ErrNoStock
		default:
			return nil, fmt.Errorf("adapter.dujiao: 上游拒绝下单 (%s): %s", resp.ErrorCode, resp.ErrorMessage)
		}
	}
	id := resp.OrderNo
	if id == "" {
		id = idString(resp.OrderID)
	}
	return &CreateOrderResult{
		UpstreamOrderID: id,
		Status:          resp.Status,
		Amount:          parseYuanToCents(resp.Amount),
	}, nil
}

func (a *dujiaoAdapter) GetOrder(ctx context.Context, upstreamOrderID string) (*OrderDetail, error) {
	path := "/api/v1/upstream/orders/" + url.PathEscape(upstreamOrderID)
	data, err := a.request(ctx, "GET", path, nil, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		OK          bool   `json:"ok"`
		OrderID     any    `json:"order_id"`
		OrderNo     string `json:"order_no"`
		Status      string `json:"status"`
		Amount      string `json:"amount"`
		Fulfillment *struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Payload string `json:"payload"`
		} `json:"fulfillment"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("adapter.dujiao: 解析订单详情失败: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("adapter.dujiao: 上游返回 ok=false")
	}
	status := resp.Status
	var cards []string
	if resp.Fulfillment != nil {
		if resp.Fulfillment.Status == "delivered" {
			status = "delivered"
		}
		cards = splitCards(resp.Fulfillment.Payload)
	}
	return &OrderDetail{
		UpstreamOrderID: upstreamOrderID,
		Status:          status,
		Amount:          parseYuanToCents(resp.Amount),
		Cards:           cards,
	}, nil
}

func (a *dujiaoAdapter) RefundOrder(ctx context.Context, upstreamOrderID string) error {
	return ErrNotSupported // 协议未开放退款端点（1.x 同款）；退款走人工/对账
}

// ListOrders 对账列表能力：协议未开放订单列表端点——对账走 GetOrder 核对模式。
func (a *dujiaoAdapter) ListOrders(ctx context.Context, start, end time.Time) ([]OrderDetail, error) {
	return nil, ErrNotSupported
}

// dujiaoProduct 上游商品行（字段对齐友商 UpstreamProduct）。
type dujiaoProduct struct {
	ID          any    `json:"id"`
	Name        string `json:"name"`
	PriceAmount string `json:"price_amount"`
	CategoryID  any    `json:"category_id"`
	Description string `json:"description"`
	IsActive    bool   `json:"is_active"`
	StockStatus string `json:"stock_status"`
	SKUs        []struct {
		ID            any    `json:"id"`
		SKUCode       string `json:"sku_code"`
		PriceAmount   string `json:"price_amount"`
		StockQuantity int32  `json:"stock_quantity"`
		IsActive      bool   `json:"is_active"`
	} `json:"skus"`
}

func (p dujiaoProduct) toProduct() Product {
	out := Product{
		ID:          idString(p.ID),
		Name:        p.Name,
		CategoryID:  idString(p.CategoryID),
		Price:       parseYuanToCents(p.PriceAmount),
		Description: p.Description,
		IsActive:    p.IsActive,
		Stock:       -1,
	}
	for _, s := range p.SKUs {
		out.SKUs = append(out.SKUs, SKU{
			ID:       idString(s.ID),
			Code:     s.SKUCode,
			Price:    parseYuanToCents(s.PriceAmount),
			Stock:    s.StockQuantity,
			IsActive: s.IsActive,
		})
	}
	return out
}

// parseYuanToCents 上游金额字符串（元，如 "12.34"）→ 分（铁律 1）。
// 空串/非法 → 0（防御；上游字段缺失不阻断同步）。
func parseYuanToCents(s string) int64 {
	if s == "" {
		return 0
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return 0
	}
	return int64(f*100 + 0.5) // 四舍五入到分（1.x 同款 round 语义）
}

// splitCards 拆分上游卡密串（acg-faka PHP_EOL / dujiao payload 按行拆分）。
// 1.x 教训：不拆则 N 张卡被当成 1 条发货记录。每行 trim 后过滤空行。
func splitCards(secret string) []string {
	if secret == "" {
		return nil
	}
	lines := splitLines(secret)
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func splitLines(s string) []string {
	// 兼容 \r\n / \r / \n（PHP_EOL 在各平台差异）
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		} else if s[i] == '\r' {
			out = append(out, s[start:i])
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
