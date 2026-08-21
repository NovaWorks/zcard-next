package supplier

// P2-10 B：dujiao-next 协议兼容层（对外供货）。
//
// 让任何 dujiao-next 站点在「站点连接」里填本站地址 + 我方发的
// (api_key, api_secret) 即可对接——不改对方一行代码。
//
// 端点（前缀 /api/v1/upstream，全部需 3 头鉴权）：
//   POST /ping | GET /categories | GET /products | GET /products/{id}
//   POST /orders | GET /orders/{id} | POST /orders/{id}/cancel
//
// 协议事实来源：dujiao-next internal/modules/upstreamapi/transport/http/*（字段/
// 错误码/金额口径逐字对齐）。金额：内部分 → 字符串元两位小数；卡密：纯文本
// \n 连接（fulfillment.payload）；余额不足：HTTP 200 + ok=false + payment_failed
// （对方客户端的钱包扣款失败口径）。
//
// 实现：原生 http.Handler（HandlePrefix 挂载）——外部协议契约不进我们的 proto/
// OpenAPI；业务核心复用 SupplyAPIService.fulfillOrder。

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	khttp "github.com/go-kratos/kratos/v3/transport/http"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
)

// dujiao 时间窗（对方 MaxTimestampSkew=60；比自家协议 300s 严格，按对方来）。
const dujiaoTimeSkew = 60

// RegisterDujiaoCompat 挂载 dujiao 兼容路由（/api/v1/upstream/；须在 SPA 兜底前注册）。
func RegisterDujiaoCompat(srv *khttp.Server, svc *SupplyAPIService) {
	srv.HandlePrefix("/api/v1/upstream/", dujiaoMux(svc))
}

// dujiaoMux 路由表（独立构造便于契约测试）。
func dujiaoMux(svc *SupplyAPIService) *http.ServeMux {
	h := &dujiaoCompat{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/upstream/ping", h.wrap(h.ping))
	mux.HandleFunc("/api/v1/upstream/categories", h.wrap(h.categories))
	mux.HandleFunc("/api/v1/upstream/products", h.wrap(h.products))
	mux.HandleFunc("/api/v1/upstream/products/", h.wrap(h.productDetail)) // /{id}
	mux.HandleFunc("/api/v1/upstream/orders", h.wrap(h.createOrder))
	mux.HandleFunc("/api/v1/upstream/orders/", h.wrap(h.orderAction)) // /{id} 与 /{id}/cancel
	return mux
}

// dujiaoCompat 兼容层处理器。
type dujiaoCompat struct {
	svc *SupplyAPIService
}

// wrap 3 头鉴权 + JSON 错误壳（成功壳由各 handler 写）。
func (h *dujiaoCompat) wrap(fn func(w http.ResponseWriter, r *http.Request, account *ent.SupplierAccount)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		account, err := h.authenticate(r)
		if err != nil {
			var ae *authError
			code, status := "invalid_signature", http.StatusUnauthorized
			if errors.As(err, &ae) {
				code = ae.Code
				if code == errUnknownKey {
					status = http.StatusForbidden
					code = "invalid_api_key"
				}
				if code == errAccountDisabled {
					status = http.StatusForbidden
					code = "user_disabled"
				}
				if code == errTimestampSkew {
					code = "timestamp_expired"
				}
			}
			writeDujiaoErr(w, status, code, err.Error())
			return
		}
		fn(w, r, account)
	}
}

// authenticate 3 头校验：解析 → ±60s → key 查账户 → protocol=dujiao_next → 验签。
func (h *dujiaoCompat) authenticate(r *http.Request) (*ent.SupplierAccount, error) {
	key := r.Header.Get("Dujiao-Next-Api-Key")
	tsStr := r.Header.Get("Dujiao-Next-Timestamp")
	sig := r.Header.Get("Dujiao-Next-Signature")
	if key == "" || tsStr == "" || sig == "" {
		return nil, newAuthError(errMissingHeaders, "缺少 Dujiao-Next-Api-Key/Timestamp/Signature 头")
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil || abs64(time.Now().Unix()-ts) > dujiaoTimeSkew {
		return nil, newAuthError(errTimestampSkew, "timestamp 非法或超出 ±60s 时间窗")
	}
	account, secret, err := h.svc.repo.AccountByKey(r.Context(), key)
	if err != nil {
		return nil, newAuthError(errUnknownKey, "未知 api_key")
	}
	if string(account.Status) != "approved" {
		return nil, newAuthError(errAccountDisabled, "账户未审核或已禁用")
	}
	// IP 白名单（空名单放行；非空须命中）
	if !ipAllowed(account.IPWhitelist, r) {
		return nil, newAuthError(errAccountDisabled, "请求 IP 不在白名单内")
	}
	if string(account.Protocol) != "dujiao_next" {
		return nil, newAuthError(errUnknownKey, "该 api_key 不是 dujiao_next 兼容账号")
	}
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(strings.NewReader(string(body))) // 恢复（后续 handler 解析需要）
	if subtle.ConstantTimeCompare([]byte(dujiaoSign(secret, r.Method, r.URL.Path, tsStr, body)), []byte(sig)) != 1 {
		return nil, newAuthError(errInvalidSig, "签名校验失败")
	}
	return account, nil
}

// ── 端点实现 ────────────────────────────────────────────────

func (h *dujiaoCompat) ping(w http.ResponseWriter, r *http.Request, account *ent.SupplierAccount) {
	balance, _ := h.svc.repo.BalanceOf(r.Context(), account.ID)
	writeDujiaoOK(w, map[string]any{
		"site_name":        dujiaoSiteName(account),
		"protocol_version": "1.0",
		"user_id":          account.ID,
		"balance":          centsToYuanStr(balance),
		"currency":         "CNY",
		"member_level":     nil,
	})
}

func (h *dujiaoCompat) categories(w http.ResponseWriter, r *http.Request, _ *ent.SupplierAccount) {
	writeDujiaoOK(w, map[string]any{"categories": []any{}})
}

func (h *dujiaoCompat) products(w http.ResponseWriter, r *http.Request, account *ent.SupplierAccount) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}
	includeInactive := q.Get("include_inactive") == "true"
	// updated_after 透传忽略（全量返回；对方 upsert 幂等无害，v1 从简）
	status := int8(1)
	if includeInactive {
		status = -1
	}
	items, total, err := h.svc.reader.ListForSupply(r.Context(), catalogport.AdminFilter{Status: status, Page: int32(page), PageSize: int32(pageSize)})
	if err != nil {
		writeDujiaoErr(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, p := range items {
		out = append(out, h.dujiaoProduct(r, account, p))
	}
	writeDujiaoOK(w, map[string]any{
		"items": out, "total": total, "page": page, "page_size": pageSize,
		"includes_inactive": includeInactive,
	})
}

func (h *dujiaoCompat) productDetail(w http.ResponseWriter, r *http.Request, account *ent.SupplierAccount) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/upstream/products/")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		writeDujiaoErr(w, http.StatusBadRequest, "bad_request", "商品 ID 非法")
		return
	}
	p, err := h.svc.reader.GetForSupply(r.Context(), id)
	if err != nil {
		writeDujiaoErr(w, http.StatusNotFound, "product_not_found", "商品不存在")
		return
	}
	writeDujiaoOK(w, map[string]any{"product": h.dujiaoProduct(r, account, *p)})
}

func (h *dujiaoCompat) createOrder(w http.ResponseWriter, r *http.Request, account *ent.SupplierAccount) {
	var req struct {
		SkuID             any    `json:"sku_id"`
		Quantity          int32  `json:"quantity"`
		DownstreamOrderNo string `json:"downstream_order_no"`
		CallbackURL       string `json:"callback_url"`
		TraceID           string `json:"trace_id"`
	}
	body, _ := io.ReadAll(r.Body)
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeDujiaoErr(w, http.StatusBadRequest, "bad_request", "请求体解析失败")
			return
		}
	}
	if req.Quantity < 1 {
		req.Quantity = 1
	}
	if req.DownstreamOrderNo == "" {
		writeDujiaoErr(w, http.StatusBadRequest, "bad_request", "downstream_order_no 必填（幂等键）")
		return
	}
	// sku_id 即我方商品 ID（products 响应里每个商品单 SKU，SKU id = 商品 id）
	productID, err := parseUintAny(req.SkuID)
	if err != nil || productID == 0 {
		writeDujiaoErr(w, http.StatusBadRequest, "sku_unavailable", "sku_id 非法")
		return
	}
	out, err := h.svc.fulfillOrder(withAccount(r, account.ID), account.ID, productID, req.Quantity, req.DownstreamOrderNo, req.CallbackURL, req.TraceID)
	if err != nil {
		writeDujiaoErr(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if out.rejected {
		switch out.errCode {
		case "insufficient_balance":
			// dujiao 钱包扣款失败口径：HTTP 200 + ok=false + payment_failed
			writeDujiaoErr(w, http.StatusOK, "payment_failed", "供货余额不足")
		case "product_unavailable":
			writeDujiaoErr(w, http.StatusBadRequest, "product_unavailable", "商品不可用")
		case "no_stock":
			writeDujiaoErr(w, http.StatusConflict, "insufficient_stock", "库存不足")
		default:
			writeDujiaoErr(w, http.StatusBadRequest, "bad_request", out.errMsg)
		}
		return
	}
	writeDujiaoOK(w, map[string]any{
		"order_id": out.order.ID, // dujiao 客户端 uint
		"order_no": out.order.DownstreamOrderNo,
		"status":   dujiaoStatus(string(out.order.Status), out.delivered),
		"amount":   centsToYuanStr(out.amount),
		"currency": "CNY",
	})
}

func (h *dujiaoCompat) orderAction(w http.ResponseWriter, r *http.Request, account *ent.SupplierAccount) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/upstream/orders/")
	if rest == "" {
		writeDujiaoErr(w, http.StatusBadRequest, "bad_request", "缺少订单 ID")
		return
	}
	idStr, action, _ := strings.Cut(rest, "/")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		writeDujiaoErr(w, http.StatusBadRequest, "bad_request", "订单 ID 非法")
		return
	}
	idNum, _ := strconv.ParseUint(idStr, 10, 64)
	ctx := withAccount(r, account.ID)
	o, err := h.svc.repo.GetSupplyOrder(ctx, id)
	if err != nil {
		writeDujiaoErr(w, http.StatusNotFound, "order_not_found", "订单不存在")
		return
	}
	if o.AccountID != account.ID {
		writeDujiaoErr(w, http.StatusNotFound, "order_not_found", "订单不存在") // 防枚举
		return
	}
	switch action {
	case "cancel":
		if string(o.Status) != "pending" && string(o.Status) != "paid" {
			writeDujiaoErr(w, http.StatusConflict, "cancel_not_allowed", "已交付订单不可取消")
			return
		}
		_ = h.svc.repo.LedgerEntry(ctx, o.AccountID, o.ID, "supply_refund", o.Amount,
			"supply_order:"+o.DownstreamOrderNo+":cancel", "dujiao 兼容取消退回")
		_ = h.svc.repo.MarkSupplyOrderRejected(ctx, o.ID)
		writeDujiaoOK(w, map[string]any{
			"order_id": idNum, "order_no": o.DownstreamOrderNo, "status": "canceled",
		})
	default: // 查单
		reply := map[string]any{
			"order_id": idNum, "order_no": o.DownstreamOrderNo,
			"status":          dujiaoStatus(string(o.Status), false),
			"amount":          centsToYuanStr(o.Amount),
			"currency":        "CNY",
			"refunded_amount": "0.00",
		}
		if string(o.Status) == "fulfilled" {
			if cards, err := h.svc.cardsPayloadOf(ctx, o); err == nil {
				reply["fulfillment"] = map[string]any{
					"type": "auto", "status": "delivered", "payload": strings.Join(cards, "\n"),
					"delivered_at": o.UpdatedAt.Format(time.RFC3339),
				}
			}
		}
		writeDujiaoOK(w, reply)
	}
}

// dujiaoProduct 商品行（单 SKU：SKU id = 商品 id；金额字符串元）。
func (h *dujiaoCompat) dujiaoProduct(r *http.Request, account *ent.SupplierAccount, p catalogport.SupplierProduct) map[string]any {
	ctx := withAccount(r, account.ID)
	price := p.Price
	if override, err := h.svc.repo.PriceOf(ctx, account.ID, p.ID, 0); err == nil && override > 0 {
		price = override
	}
	stock := -1
	if st, err := h.svc.inv.Stock(ctx, p.ID, 0); err == nil {
		stock = int(st)
	}
	stockStatus := "in_stock"
	if stock == 0 {
		stockStatus = "out_of_stock"
	}
	return map[string]any{
		"id":               p.ID, // dujiao 客户端 uint（字符串 unmarshal 失败）
		"slug":             fmt.Sprintf("p-%d", p.ID),
		"seo_meta":         map[string]any{},
		"title":            map[string]any{"zh-CN": p.Name}, // dujiao 多语言对象（jsonmap.JSON）
		"description":      map[string]any{"zh-CN": p.Description},
		"content":          map[string]any{"zh-CN": p.Description},
		"images":           imgList(p.Cover),
		"price_amount":     centsToYuanStr(price),
		"currency":         "CNY",
		"fulfillment_type": "auto",
		"is_active":        p.Status == 1,
		"category_id":      p.CategoryID,
		"updated_at":       time.Now().UTC().Format(time.RFC3339), // 客户端不消费（增量信任上游过滤）；对齐协议字段
		"skus": []map[string]any{{
			"id": p.ID, "sku_code": "default",
			"price_amount": centsToYuanStr(price),
			"stock_status": stockStatus, "stock_quantity": stock,
			"is_active": p.Status == 1,
		}},
	}
}

// ── 工具 ───────────────────────────────────────────────────

// dujiaoStatus 我方状态 → dujiao 口径。
func dujiaoStatus(status string, deliveredNow bool) string {
	if deliveredNow {
		return "delivered"
	}
	switch status {
	case "fulfilled":
		return "delivered"
	case "rejected":
		return "canceled"
	case "refunded", "refunding":
		return "refunded"
	default:
		return "paid"
	}
}

// centsToYuanStr 分 → "12.34"（字符串元两位小数；dujiao 全端点口径）。
func centsToYuanStr(cents int64) string {
	neg := ""
	if cents < 0 {
		neg, cents = "-", -cents
	}
	return fmt.Sprintf("%s%d.%02d", neg, cents/100, cents%100)
}

// dujiaoSiteName connect/ping 回显店铺名。
func dujiaoSiteName(account *ent.SupplierAccount) string {
	if account.DisplayName != "" {
		return account.DisplayName
	}
	return "ZCard Supply"
}

func writeDujiaoOK(w http.ResponseWriter, data map[string]any) {
	data["ok"] = true
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

func writeDujiaoErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": code, "error_message": msg})
}

// withAccount 注入账户上下文（fulfillOrder/仓库层消费 SupplyAccountID）。
func withAccount(r *http.Request, accountID uint64) context.Context {
	return context.WithValue(r.Context(), accountCtxKey{}, accountID)
}

// parseUintAny JSON 数值/字符串 → uint64。
func parseUintAny(v any) (uint64, error) {
	switch x := v.(type) {
	case float64:
		return uint64(x), nil
	case string:
		return strconv.ParseUint(x, 10, 64)
	case json.Number:
		return strconv.ParseUint(x.String(), 10, 64)
	case uint64:
		return x, nil
	case int:
		if x < 0 {
			return 0, fmt.Errorf("负数")
		}
		return uint64(x), nil
	}
	return 0, fmt.Errorf("无法解析 %T", v)
}

// imgList 封面 → images 数组（dujiao 字段形态）。
func imgList(cover string) []string {
	if cover == "" {
		return []string{}
	}
	return []string{cover}
}
