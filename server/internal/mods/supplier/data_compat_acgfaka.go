package supplier

// P2-10 C：acg-faka 协议兼容层（对外供货）。
//
// 让任何 acg-faka 站点用「共享店铺 → 对接异次元(type=0)」填本站地址 +
// (app_id, app_key) 即可对接——不改对方一行代码。
//
// 端点（前缀 /shared，全部 POST form-urlencoded、无路径参数——acg Kernel 按
// "/" 拆段路由）：
//   /shared/authentication/connect                     → {shopName, balance(元)}
//   /shared/commodity/items                            → 两级分类树 children=[商品]
//   /shared/commodity/item (code)                      → 单商品
//   /shared/commodity/stock (code,race,sku)            → {stock}
//   /shared/commodity/valuation (code,num,...)         → {price(元)}
//   /shared/commodity/trade (shared_code,num,request_no,...) → {url,amount,tradeNo,secret}
//   /shared/commodity/query (tradeNo)                  → {secret,widget,status}
//
// 协议事实来源：acg-faka app/Controller/Shared/{Commodity,Authentication}.php +
// app/Service/Bind/Shared.php（字段/签名/错误口径逐字对齐）：
//   - 鉴权：body 内 app_id + app_key + sign（MD5，无时间戳/nonce——协议固有，
//     以 request_no 幂等 + HTTPS + 账号限流补偿，见计划 §C4）
//   - 响应：{code:200,msg,data}；失败 {code:0,msg}（HTTP 200，无 data，字段名 msg）
//   - 金额：元（数字）；卡密：secret 纯文本 \n 连接
//
// 实现：原生 http.Handler（HandlePrefix 挂载）；核心复用 fulfillOrder。

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	khttp "github.com/go-kratos/kratos/v3/transport/http"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
)

// acgOrderPrefix acg 幂等键命名空间（与 zcard/dujiao 下游单号隔离）。
const acgOrderPrefix = "acg:"

// RegisterAcgFakaCompat 挂载 acg-faka 兼容路由（/shared/；须在 SPA 兜底前注册）。
func RegisterAcgFakaCompat(srv *khttp.Server, svc *SupplyAPIService) {
	srv.HandlePrefix("/shared/", acgMux(svc))
}

// acgMux 路由表（独立构造便于契约测试）。
func acgMux(svc *SupplyAPIService) *http.ServeMux {
	h := &acgCompat{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("/shared/authentication/connect", h.wrap(h.connect))
	mux.HandleFunc("/shared/commodity/items", h.wrap(h.items))
	mux.HandleFunc("/shared/commodity/item", h.wrap(h.item))
	mux.HandleFunc("/shared/commodity/stock", h.wrap(h.stock))
	mux.HandleFunc("/shared/commodity/valuation", h.wrap(h.valuation))
	mux.HandleFunc("/shared/commodity/trade", h.wrap(h.trade))
	mux.HandleFunc("/shared/commodity/query", h.wrap(h.query))
	return mux
}

type acgCompat struct {
	svc *SupplyAPIService
}

// wrap body MD5 验签（app_id → 账户 → protocol 校验 → 解密 app_key 重算比对）。
func (h *acgCompat) wrap(fn func(w http.ResponseWriter, r *http.Request, account *ent.SupplierAccount, form map[string]string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAcgErr(w, "仅支持 POST")
			return
		}
		if err := r.ParseForm(); err != nil {
			writeAcgErr(w, "表单解析失败")
			return
		}
		form := map[string]string{}
		for k, v := range r.PostForm {
			if len(v) > 0 {
				form[k] = v[0]
			}
		}
		appID := form["app_id"]
		sign := form["sign"]
		if appID == "" || sign == "" {
			writeAcgErr(w, "商户ID不存在")
			return
		}
		account, secret, err := h.svc.repo.AccountByKey(r.Context(), appID)
		if err != nil {
			writeAcgErr(w, "商户ID不存在")
			return
		}
		if string(account.Status) != "approved" {
			writeAcgErr(w, "商户被禁用")
			return
		}
		if string(account.Protocol) != "acg_faka" {
			writeAcgErr(w, "商户ID不存在") // 非 acg 账号不暴露
			return
		}
		if !acgFakaSignVerify(form, secret, sign) {
			writeAcgErr(w, "密钥错误")
			return
		}
		fn(w, r, account, form)
	}
}

// ── 端点实现 ────────────────────────────────────────────────

func (h *acgCompat) connect(w http.ResponseWriter, r *http.Request, account *ent.SupplierAccount, _ map[string]string) {
	balance, _ := h.svc.repo.BalanceOf(r.Context(), account.ID)
	name := account.DisplayName
	if name == "" {
		name = "ZCard Supply"
	}
	writeAcgOK(w, map[string]any{"shopName": name, "balance": centsToYuanFloat(balance)})
}

func (h *acgCompat) items(w http.ResponseWriter, r *http.Request, account *ent.SupplierAccount, _ map[string]string) {
	ctx := withAccount(r, account.ID)
	cats, err := h.svc.reader.ListSupplyCategories(ctx)
	if err != nil {
		writeAcgErr(w, "分类读取失败")
		return
	}
	prods, _, err := h.svc.reader.ListForSupply(ctx, catalogport.AdminFilter{Status: -1, Page: 1, PageSize: 1000})
	if err != nil {
		writeAcgErr(w, "商品读取失败")
		return
	}
	// 两级树：分类 → children 商品；未分类商品归入「默认分类」（id=0）
	byCat := map[uint64][]map[string]any{}
	for _, p := range prods {
		byCat[p.CategoryID] = append(byCat[p.CategoryID], h.acgProduct(ctx, account, p))
	}
	tree := make([]map[string]any, 0, len(cats)+1)
	if uncategorized := byCat[0]; len(uncategorized) > 0 {
		tree = append(tree, map[string]any{"id": 0, "name": "默认分类", "children": uncategorized})
	}
	for _, c := range cats {
		tree = append(tree, map[string]any{"id": c.ID, "name": c.Name, "children": orEmptyList(byCat[c.ID])})
	}
	writeAcgOK(w, tree)
}

func (h *acgCompat) item(w http.ResponseWriter, r *http.Request, account *ent.SupplierAccount, form map[string]string) {
	p, ok := h.supplyProduct(r, account, form["code"])
	if !ok {
		writeAcgErr(w, "商品不存在")
		return
	}
	writeAcgOK(w, h.acgProduct(withAccount(r, account.ID), account, *p))
}

func (h *acgCompat) stock(w http.ResponseWriter, r *http.Request, account *ent.SupplierAccount, form map[string]string) {
	p, ok := h.supplyProduct(r, account, form["code"])
	if !ok {
		writeAcgErr(w, "商品不存在")
		return
	}
	ctx := withAccount(r, account.ID)
	stock := -1
	if st, err := h.svc.inv.Stock(ctx, p.ID, 0); err == nil {
		stock = int(st)
	}
	writeAcgOK(w, map[string]any{"stock": stock})
}

func (h *acgCompat) valuation(w http.ResponseWriter, r *http.Request, account *ent.SupplierAccount, form map[string]string) {
	p, ok := h.supplyProduct(r, account, form["code"])
	if !ok {
		writeAcgErr(w, "商品不存在")
		return
	}
	num := atoiOr(form["num"], 1)
	if num < 1 {
		num = 1
	}
	ctx := withAccount(r, account.ID)
	writeAcgOK(w, map[string]any{"price": centsToYuanFloat(h.supplyPrice(ctx, account, *p) * int64(num))})
}

func (h *acgCompat) trade(w http.ResponseWriter, r *http.Request, account *ent.SupplierAccount, form map[string]string) {
	code := form["shared_code"]
	num := atoiOr(form["num"], 1)
	requestNo := form["request_no"]
	if code == "" || requestNo == "" {
		writeAcgErr(w, "参数缺失（shared_code/request_no 必填）")
		return
	}
	p, ok := h.supplyProduct(r, account, code)
	if !ok || p.Status != 1 {
		writeAcgErr(w, "商品不存在或已下架")
		return
	}
	out, err := h.svc.fulfillOrder(withAccount(r, account.ID), account.ID, p.ID, int32(num),
		acgOrderPrefix+requestNo, "", "acg-"+requestNo) // acg 协议无回调
	if err != nil {
		writeAcgErr(w, "下单失败，请稍后重试")
		return
	}
	if out.rejected {
		switch out.errCode {
		case "insufficient_balance":
			writeAcgErr(w, "余额不足")
		case "no_stock":
			writeAcgErr(w, "库存不足")
		default:
			writeAcgErr(w, "商品不可用")
		}
		return
	}
	if !out.delivered {
		// 幂等重放未交付首单（历史单 pending/paid）——acg 只有同步交付口径
		writeAcgErr(w, "订单处理中，请勿重复提交")
		return
	}
	writeAcgOK(w, map[string]any{
		"url":     nil,
		"amount":  centsToYuanFloat(out.amount),
		"tradeNo": strconv.FormatUint(out.order.ID, 10),
		"secret":  strings.Join(out.cards, "\n"),
	})
}

func (h *acgCompat) query(w http.ResponseWriter, r *http.Request, account *ent.SupplierAccount, form map[string]string) {
	id, err := strconv.ParseUint(form["tradeNo"], 10, 64)
	if err != nil {
		writeAcgErr(w, "订单号非法")
		return
	}
	ctx := withAccount(r, account.ID)
	o, err := h.svc.repo.GetSupplyOrder(ctx, id)
	if err != nil || o.AccountID != account.ID {
		writeAcgErr(w, "订单不存在")
		return
	}
	status := 0 // 0=未完成 1=已支付
	if string(o.Status) != "pending" && string(o.Status) != "rejected" {
		status = 1
	}
	secret := ""
	if string(o.Status) == "fulfilled" {
		if cards, err := h.svc.cardsPayloadOf(ctx, o); err == nil {
			secret = strings.Join(cards, "\n")
		}
	}
	writeAcgOK(w, map[string]any{"secret": secret, "widget": nil, "status": status})
}

// ── 工具 ───────────────────────────────────────────────────

// supplyProduct code（商品 ID）→ 供货商品。
func (h *acgCompat) supplyProduct(r *http.Request, account *ent.SupplierAccount, code string) (*catalogport.SupplierProduct, bool) {
	id, err := strconv.ParseUint(code, 10, 64)
	if err != nil || id == 0 {
		return nil, false
	}
	p, err := h.svc.reader.GetForSupply(withAccount(r, account.ID), id)
	if err != nil {
		return nil, false
	}
	return p, true
}

// supplyPrice 供货价（覆盖价 > 基础价）。
func (h *acgCompat) supplyPrice(ctx context.Context, account *ent.SupplierAccount, p catalogport.SupplierProduct) int64 {
	if override, err := h.svc.repo.PriceOf(ctx, account.ID, p.ID, 0); err == nil && override > 0 {
		return override
	}
	return p.Price
}

// acgProduct 商品行（字段对齐 acg commodity 语义；金额元字符串）。
// 字段集合覆盖 acg 3.5.7+ 客户端 sync() 的读取全集——draft/seckill/contact
// 为常量语义：本站不支持预选卡与秒杀，与 draft_status/seckill_status=0 一致；
// contact_type=0（任意）避免对接站把 null 写进 NOT NULL 列而同步失败。
func (h *acgCompat) acgProduct(ctx context.Context, account *ent.SupplierAccount, p catalogport.SupplierProduct) map[string]any {
	price := h.supplyPrice(ctx, account, p)
	stock := -1
	if st, err := h.svc.inv.Stock(ctx, p.ID, 0); err == nil {
		stock = int(st)
	}
	return map[string]any{
		"code":               strconv.FormatUint(p.ID, 10),
		"name":               p.Name,
		"price":              centsToYuanStr(price),
		"user_price":         centsToYuanStr(price),
		"factory_price":      centsToYuanStr(p.FactoryPrice),
		"description":        p.Description,
		"introduce":          p.Description,
		"cover":              p.Cover,
		"status":             boolToInt(p.Status == 1),
		"delivery_way":       1, // 自动发货
		"draft_status":       0,
		"draft_premium":      "0",
		"minimum":            1,
		"maximum":            99,
		"stock":              stock,
		"config":             "",
		"widget":             nil,
		"category_id":        p.CategoryID,
		"seckill_status":     0,
		"seckill_start_time": nil,
		"seckill_end_time":   nil,
		"contact_type":       0,
	}
}

func writeAcgOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "success", "data": data})
}

func writeAcgErr(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": msg})
}

// centsToYuanFloat 分 → 元数字（acg connect/valuation/trade amount 口径）。
func centsToYuanFloat(cents int64) float64 {
	return float64(cents) / 100
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}

func orEmptyList(list []map[string]any) []map[string]any {
	if list == nil {
		return []map[string]any{}
	}
	return list
}
