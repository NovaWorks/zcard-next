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
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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
		ID       any    `json:"id"`
		Name     string `json:"name"`
		Children []struct {
			Code         string `json:"code"`
			Name         string `json:"name"`
			Price        FlexNum `json:"price"`
			FactoryPrice FlexNum `json:"factory_price"`
			Description  string `json:"description"`
			Introduce    string `json:"introduce"`
			Cover        string `json:"cover"`
			Status       int    `json:"status"` // 1=上架 0=下架
			Stock        *int32 `json:"stock"`  // 仅自动发货商品有；手动发货缺省
			CategoryID   any    `json:"category_id"`
			DeliveryWay  int    `json:"delivery_way"`
			Config       string `json:"config"` // INI 字符串（items 直出原始 INI；item 接口为数组——本适配器只消费 items）
		} `json:"children"`
	}
	if err := json.Unmarshal(raw, &cats); err != nil {
		return nil, fmt.Errorf("adapter.acgfaka: 解析商品列表失败: %w", err)
	}
	// items 一次全量（无分页）：快照天然含下架商品（is_active 过滤语义下放）→ 对账权威
	out := &ProductList{IncludesInactive: true}
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
			// 手动发货商品（delivery_way=1）不随 API 上架：其订单查询恒返回
			// 占位文案（Bind/Order.php:1231 delivery_message/「正在发货中…」），
			// API 通道永远拿不到真实卡密——同步为下架，需售卖请本地建品人工发货
			active := p.Status == 1 && p.DeliveryWay == 0

			// 多规格（[category] race + [sku] 加价规格）→ 笛卡尔积组合 SKU
			// （组合超护栏/含编码保留字符 → 整品隐藏防误售，价格语义不完整）
			var skus []SKU
			ini, _ := parseAcgINI(p.Config)
			combos, comboErr := buildAcgCombos(ini, p.Price.Cents())
			if comboErr != nil {
				active = false
			} else {
				for _, c := range combos {
					skus = append(skus, SKU{
						ID:         c.Code,
						Code:       c.Code,
						Name:       c.Name,
						Price:      c.BaseCents + c.AddCents,
						Stock:      -1,
						IsActive:   true,
						SpecValues: specValuesOf(c),
					})
				}
			}
			out.Items = append(out.Items, Product{
				ID:           p.Code,
				Name:         p.Name,
				CategoryID:   catID,
				Price:        p.Price.Cents(),
				FactoryPrice: p.FactoryPrice.Cents(),
				Description:  desc,
				Cover:        p.Cover,
				IsActive:     active,
				Stock:        stock,
				SKUs:         skus,
			})
		}
	}
	out.Total = len(out.Items)
	return out, nil
}

func (a *acgFakaAdapter) GetStock(ctx context.Context, productCode, skuCode string) (int32, error) {
	params := map[string]string{"code": productCode}
	if skuCode != "" {
		fields, err := AcgSpecFormFields(skuCode)
		if err != nil {
			return 0, fmt.Errorf("adapter.acgfaka: 规格编码非法: %w", err)
		}
		for k, v := range fields {
			params[k] = v
		}
	}
	data, err := a.signedPost(ctx, "/shared/commodity/stock", params)
	if err != nil {
		return 0, err
	}
	raw, err := parseResp(data)
	if err != nil {
		return 0, err
	}
	var d struct {
		Stock FlexNum `json:"stock"` // PHP 侧 string 直出（{"stock":"990"}），FlexNum 兼容两种形态
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return 0, fmt.Errorf("adapter.acgfaka: 解析库存失败: %w", err)
	}
	return int32(d.Stock), nil
}

// acgPlaceholderTexts 上游「非真实卡密」文案（手动发货占位 / 付款后库存被抢）。
// 出现在 secret 里时绝不能当卡密交付（Bind/Order.php:1231/1278 原文）。
var acgPlaceholderTexts = []string{
	"正在发货中，请耐心等待，如有疑问，请联系客服。",
	"很抱歉，有人在你付款之前抢走了商品，请联系客服。",
}

// dropPlaceholderCards 过滤占位文案行（全等匹配；自定义 delivery_message 无法枚举，
// 由同步侧 delivery_way 过滤兜底）。
func dropPlaceholderCards(cards []string) []string {
	out := make([]string, 0, len(cards))
	for _, c := range cards {
		placeholder := false
		for _, t := range acgPlaceholderTexts {
			if strings.TrimSpace(c) == t {
				placeholder = true
				break
			}
		}
		if !placeholder {
			out = append(out, c)
		}
	}
	return out
}

func (a *acgFakaAdapter) CreateOrder(ctx context.Context, req CreateOrderReq) (*CreateOrderResult, error) {
	params := map[string]string{
		"shared_code": req.ProductCode,
		"num":         strconv.Itoa(req.Quantity),
		"contact":     req.TraceID,
		"request_no":  req.DownstreamOrderNo, // 防重键（上游重复即报错，窗口见 doc.go）
		"card_id":     "0",
	}
	// 多规格：本地 SKU 携带的选择编码 → race + sku[规格名] 表单字段
	// （PHP 侧把 sku[名] 解析回嵌套数组；我方按字母序发送 == 签名 ksort 序，
	//  与服务端 http_build_query 输出字节一致——黄金向量锁死）
	if req.UpstreamSKU != "" {
		fields, err := AcgSpecFormFields(req.UpstreamSKU)
		if err != nil {
			return nil, fmt.Errorf("adapter.acgfaka: 规格编码非法: %w", err)
		}
		for k, v := range fields {
			params[k] = v
		}
	}
	data, err := a.signedPost(ctx, "/shared/commodity/trade", params)
	if err != nil {
		return nil, err
	}
	raw, err := parseResp(data)
	if err != nil {
		// request_no 是「重复即报错」的防重键（非幂等回显）：报此错说明同键请求
		// 已被上游受理过——可能首响应在网络层丢失且上游已扣款发货，绝不能
		// 重试（会永远撞墙）也不能自动退款（上游可能已成交）→ 哨兵转人工核对
		if strings.Contains(err.Error(), "request ID already exists") {
			return nil, ErrDuplicateSubmit
		}
		return nil, err
	}
	var d struct {
		TradeNo string  `json:"tradeNo"`
		Amount  FlexNum `json:"amount"` // 元（数字或字符串——MySQL DECIMAL 直出实况）
		Secret  string  `json:"secret"` // PHP_EOL 拼的多卡
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("adapter.acgfaka: 解析下单响应失败: %w", err)
	}
	cards := dropPlaceholderCards(splitCards(d.Secret))
	status := "pending"
	if len(cards) > 0 {
		status = "delivered"
	}
	return &CreateOrderResult{
		// 必须存上游 tradeNo：query 接口按 trade_no 匹配（1.x 教训 #2）
		UpstreamOrderID: d.TradeNo,
		Status:          status,
		Amount:          d.Amount.Cents(),
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
	cards := dropPlaceholderCards(splitCards(d.Secret))
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

// specValuesOf 组合 → 结构化规格值（种类 + 各规格名=选项）。
func specValuesOf(c acgCombo) map[string]string {
	out := make(map[string]string, len(c.Choices)+1)
	if c.Race != "" {
		out["种类"] = c.Race
	}
	for k, v := range c.Choices {
		out[k] = v
	}
	return out
}
