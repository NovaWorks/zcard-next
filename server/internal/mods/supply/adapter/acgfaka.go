package adapter

// acgfaka.go — acg-faka（异次元发卡）协议（1.x CLAUDE.md 协议知识迁移）。
//
// 已知上游限制（适配器侧规避，1.x 教训）：
//   - 路由按 / 拆段：路径参数只能用 body（如 query 的 tradeNo），绝不能拼路径；
//   - cards 用 PHP_EOL 拼成一个 secret 串 → 必须拆分（splitCards）；
//   - request_no 是「重复即报错」的防重键而非可恢复幂等键 → 重试窗口见 doc.go；
//   - verifyCallback 恒 null → 只能同步拿货（trade 后直接取 secret）；
//   - sign 在 body 内（app_id + app_key + sign 全放表单）；
//   - 不跟随重定向（allow_redirects=false 语义：app_key 在 body 里，307 会转发凭据）。

import (
	"context"
	"time"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
)

// acgFakaAdapter acg-faka 协议适配器。
type acgFakaAdapter struct {
	protocol string
	creds    Credentials
	t        *transport
}

func newAcgFaka(baseURL string, creds Credentials, retryIntervals []int) (Adapter, error) {
	t, err := newTransport(baseURL, retryIntervals, slog.Default())
	if err != nil {
		return nil, err
	}
	return &acgFakaAdapter{protocol: "acg_faka", creds: creds, t: t}, nil
}

func (a *acgFakaAdapter) Protocol() string { return a.protocol }

// signedParams 构造带签名的表单参数（app_id + app_key + sign 都放 body）。
func (a *acgFakaAdapter) signedParams(params map[string]string) map[string]string {
	out := make(map[string]string, len(params)+3)
	for k, v := range params {
		out[k] = v
	}
	out["app_id"] = a.creds.AppID
	out["app_key"] = a.creds.AppKey
	out["sign"] = AcgFakaSign(out, a.creds.AppKey)
	return out
}

// signedPost 表单 POST（application/x-www-form-urlencoded）。
func (a *acgFakaAdapter) signedPost(ctx context.Context, path string, params map[string]string) ([]byte, error) {
	form := a.signedParams(params)
	encoded := url.Values{}
	for k, v := range form {
		encoded.Set(k, v)
	}
	body := []byte(encoded.Encode())
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	return a.t.do(ctx, http.MethodPost, path, nil, headers, body)
}

// acgResp 上游统一响应 {code, msg, data}；code=200 成功。
type acgResp struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// parseResp 校验 code=200 并返回 data。
func parseResp(raw []byte) (json.RawMessage, error) {
	var resp acgResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("adapter.acgfaka: 解析响应失败: %w", err)
	}
	if resp.Code != 200 {
		// 业务错误归一化：余额不足/无库存等明确语义
		switch resp.Code {
		case 401, 403:
			return nil, fmt.Errorf("adapter.acgfaka: 商户鉴权失败(%d): %s", resp.Code, resp.Msg)
		default:
			return nil, fmt.Errorf("adapter.acgfaka: 上游拒绝(%d): %s", resp.Code, resp.Msg)
		}
	}
	return resp.Data, nil
}

func (a *acgFakaAdapter) Ping(ctx context.Context) (*PingResult, error) {
	// connect 端点：返回 {site_name, ...}
	data, err := a.signedPost(ctx, "/shared/authentication/connect", nil)
	if err != nil {
		return nil, err
	}
	raw, err := parseResp(data)
	if err != nil {
		return nil, err
	}
	var d struct {
		SiteName string `json:"site_name"`
	}
	_ = json.Unmarshal(raw, &d)
	return &PingResult{SiteName: d.SiteName, Balance: -1}, nil // acg-faka 无余额口径
}

func (a *acgFakaAdapter) ListCategories(ctx context.Context) ([]Category, error) {
	// 1.x 已知限制：listCategories 返回 []，分类只能靠 items 的 cat.id 反推。
	return []Category{}, nil
}

func (a *acgFakaAdapter) ListProducts(ctx context.Context, page, _ int, includeInactive bool) (*ProductList, error) {
	// items 一次全量（无分页）；includeInactive 恒返回全部商品，由 is_active 过滤语义下放
	data, err := a.signedPost(ctx, "/shared/commodity/items", nil)
	if err != nil {
		return nil, err
	}
	raw, err := parseResp(data)
	if err != nil {
		return nil, err
	}
	var cats []struct {
		ID       any `json:"id"`
		Name     string `json:"name"`
		Children []struct {
			Code          string `json:"code"`
			Name          string `json:"name"`
			Price         string `json:"price"`
			FactoryPrice  string `json:"factory_price"`
			Description   string `json:"description"`
			Introduce     string `json:"introduce"`
			Cover         string `json:"cover"`
			Status        int    `json:"status"` // 1=上架 0=下架
			Stock         *int32 `json:"stock"`  // 仅自动发货商品有；手动发货缺省
			CategoryID    any    `json:"category_id"`
			DeliveryWay   int    `json:"delivery_way"`
		} `json:"children"`
	}
	if err := json.Unmarshal(raw, &cats); err != nil {
		return nil, fmt.Errorf("adapter.acgfaka: 解析商品列表失败: %w", err)
	}
	out := &ProductList{}
	for _, cat := range cats {
		catID := idString(cat.ID)
		for _, p := range cat.Children {
			stock := int32(-1)
			if p.Stock != nil {
				stock = *p.Stock
			}
			desc := p.Description
			if desc == "" {
				desc = p.Introduce
			}
			out.Items = append(out.Items, Product{
				ID:           p.Code,
				Name:         p.Name,
				CategoryID:   catID,
				Price:        parseYuanToCents(p.Price),
				FactoryPrice: parseYuanToCents(p.FactoryPrice),
				Description:  desc,
				Cover:        p.Cover,
				IsActive:     p.Status == 1,
				Stock:        stock,
			})
		}
	}
	out.Total = len(out.Items)
	return out, nil
}

func (a *acgFakaAdapter) GetStock(ctx context.Context, productCode, _ string) (int32, error) {
	data, err := a.signedPost(ctx, "/shared/commodity/stock", map[string]string{"code": productCode})
	if err != nil {
		return 0, err
	}
	raw, err := parseResp(data)
	if err != nil {
		return 0, err
	}
	var d struct {
		Stock int32 `json:"stock"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return 0, fmt.Errorf("adapter.acgfaka: 解析库存失败: %w", err)
	}
	return d.Stock, nil
}

func (a *acgFakaAdapter) CreateOrder(ctx context.Context, req CreateOrderReq) (*CreateOrderResult, error) {
	data, err := a.signedPost(ctx, "/shared/commodity/trade", map[string]string{
		"shared_code": req.ProductCode,
		"num":         strconv.Itoa(req.Quantity),
		"contact":     req.TraceID,
		"request_no":  req.DownstreamOrderNo, // 防重键（上游重复即报错，窗口见 doc.go）
		"card_id":     "0",
	})
	if err != nil {
		return nil, err
	}
	raw, err := parseResp(data)
	if err != nil {
		return nil, err
	}
	var d struct {
		TradeNo string `json:"tradeNo"`
		Amount  string `json:"amount"` // 元（字符串）
		Secret  string `json:"secret"` // PHP_EOL 拼的多卡
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("adapter.acgfaka: 解析下单响应失败: %w", err)
	}
	cards := splitCards(d.Secret)
	status := "pending"
	if len(cards) > 0 {
		status = "delivered"
	}
	return &CreateOrderResult{
		// 必须存上游 tradeNo：query 接口按 trade_no 匹配（1.x 教训 #2）
		UpstreamOrderID: d.TradeNo,
		Status:          status,
		Amount:          parseYuanToCents(d.Amount),
		Cards:           cards,
	}, nil
}

func (a *acgFakaAdapter) GetOrder(ctx context.Context, upstreamOrderID string) (*OrderDetail, error) {
	// 路径参数只能走 body（Kernel 按 / 拆段，/query/{no} 恒 404，1.x 教训 #1）
	data, err := a.signedPost(ctx, "/shared/commodity/query", map[string]string{"tradeNo": upstreamOrderID})
	if err != nil {
		return nil, err
	}
	raw, err := parseResp(data)
	if err != nil {
		return nil, err
	}
	var d struct {
		Status int    `json:"status"` // 0=未完成 1=已支付
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("adapter.acgfaka: 解析订单详情失败: %w", err)
	}
	cards := splitCards(d.Secret)
	// 必须 status=1 才认发货（1.x 教训：否则凭 fulfillment 就写卡）
	status := "pending"
	if d.Status == 1 && len(cards) > 0 {
		status = "delivered"
	}
	return &OrderDetail{
		UpstreamOrderID: upstreamOrderID,
		Status:          status,
		Cards:           cards,
	}, nil
}

func (a *acgFakaAdapter) RefundOrder(ctx context.Context, _ string) error {
	// 上游无退款端点（1.x 同款：cancelOrder 返回 false）
	return ErrNotSupported
}

// ListOrders 对账列表能力：协议未开放订单列表端点。
func (a *acgFakaAdapter) ListOrders(ctx context.Context, start, end time.Time) ([]OrderDetail, error) {
	return nil, ErrNotSupported
}
