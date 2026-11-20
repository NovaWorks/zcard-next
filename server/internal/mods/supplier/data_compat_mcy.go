package supplier

// MCY OpenApi compatibility, based on the existing acg-faka MCY client.
// Native MCY downstream sites can also use their official MCYShopV3 supply
// plugin through the legacy /shared routes (see data_compat_acgfaka.go).
import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/mcysign"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

var errMCYStock = errors.New("萌次元供货库存读取失败")

func RegisterMCYCompat(srv *khttp.Server, svc *SupplyAPIService) {
	srv.HandlePrefix("/plugin/open-api/", mcyMux(svc))
}

type mcyCompat struct{ svc *SupplyAPIService }

func mcyMux(svc *SupplyAPIService) *http.ServeMux {
	h := &mcyCompat{svc: svc}
	mux := http.NewServeMux()
	for _, endpoint := range []string{"connect", "items", "item", "sku/stock", "sku/state", "amount", "trade"} {
		mux.HandleFunc("POST /plugin/open-api/"+endpoint, h.serve)
	}
	return mux
}

func (h *mcyCompat) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		writeAcgErr(w, "表单解析失败")
		return
	}
	form := map[string]string{}
	for k, values := range r.PostForm {
		// Native MCY excludes arrays from its signature. No business endpoint
		// here accepts arrays; reject them instead of processing unsigned data.
		if len(values) != 1 || strings.ContainsAny(k, "[]") {
			writeAcgErr(w, "表单参数非法")
			return
		}
		form[k] = values[0]
	}
	key, signature := r.Header.Get("Api-Id"), r.Header.Get("Api-Signature")
	account, secret, err := h.svc.repo.AccountByKey(r.Context(), key)
	if key == "" || signature == "" || err != nil || string(account.Protocol) != "mcy_shop" ||
		string(account.Status) != "approved" || !ipAllowed(account.IPWhitelist, r) || !mcysign.Verify(form, secret, signature) {
		writeAcgErr(w, "账号不可用或签名错误")
		return
	}
	ctx := withAccount(r, account.ID)
	currency, err := data.BaseCurrency(ctx, h.svc.repo.data)
	if err != nil || currency != "CNY" {
		writeAcgErr(w, "此协议仅支持人民币计价")
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/plugin/open-api/") {
	case "connect":
		balance, err := h.svc.repo.BalanceOf(ctx, account.ID)
		if err != nil {
			writeAcgErr(w, "余额读取失败")
			return
		}
		writeAcgOK(w, map[string]any{"username": dujiaoSiteName(account), "balance": centsToYuanStr(balance)})
	case "items":
		pricing, err := h.svc.repo.LoadPricing(ctx, account.ID)
		if err != nil {
			writeAcgErr(w, "供货定价读取失败")
			return
		}
		cats, err := h.svc.reader.ListSupplyCategories(ctx)
		if err != nil {
			writeAcgErr(w, "分类读取失败")
			return
		}
		catNames := map[uint64]string{}
		for _, cat := range cats {
			catNames[cat.ID] = cat.Name
		}
		out := make([]map[string]any, 0)
		for page := int32(1); ; page++ {
			items, total, err := h.svc.reader.ListForSupply(ctx, catalogport.AdminFilter{Status: 1, Page: page, PageSize: 100})
			if err != nil {
				writeAcgErr(w, "商品读取失败")
				return
			}
			for _, p := range items {
				item, err := h.product(r, p, pricing, catNames[p.CategoryID])
				if err != nil {
					writeAcgErr(w, "商品库存读取失败")
					return
				}
				out = append(out, item)
			}
			if len(items) == 0 || int64(page)*100 >= total {
				break
			}
		}
		writeAcgOK(w, out)
	case "trade":
		id, idErr := strconv.ParseUint(form["sku_id"], 10, 64)
		n, nErr := strconv.ParseInt(form["quantity"], 10, 32)
		tradeNo := form["trade_no"]
		if idErr != nil || id == 0 || nErr != nil || n < 1 || tradeNo == "" || len(tradeNo) > 60 {
			writeAcgErr(w, "商品、数量或订单号非法")
			return
		}
		out, err := h.svc.fulfillOrder(ctx, account.ID, id, int32(n), "mcy:"+tradeNo, "", "")
		if err != nil {
			writeAcgErr(w, "下单失败，请核对原订单后重试")
			return
		}
		if out.rejected {
			writeAcgErr(w, out.errMsg)
			return
		}
		if !out.delivered {
			writeAcgErr(w, "订单处理中，请勿重复提交")
			return
		}
		writeAcgOK(w, map[string]any{"contents": strings.Join(out.cards, "\n")})
	default:
		idText := form["sku_id"]
		if strings.HasSuffix(r.URL.Path, "/item") {
			idText = form["id"]
		}
		p, ok := (&acgCompat{svc: h.svc}).supplyProduct(r, account, idText)
		if !ok || p.Status != 1 {
			writeAcgErr(w, "商品不存在或已下架")
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/item"):
			pricing, err := h.svc.repo.LoadPricing(ctx, account.ID)
			if err != nil {
				writeAcgErr(w, "供货定价读取失败")
				return
			}
			item, err := h.product(r, *p, pricing, "")
			if err != nil {
				writeAcgErr(w, "商品库存读取失败")
				return
			}
			writeAcgOK(w, item)
		case strings.HasSuffix(r.URL.Path, "/stock"), strings.HasSuffix(r.URL.Path, "/state"):
			stock, err := h.svc.inv.Stock(ctx, p.ID, 0)
			if err != nil || stock < 0 {
				writeAcgErr(w, "库存暂不可用")
				return
			}
			if strings.HasSuffix(r.URL.Path, "/stock") {
				writeAcgOK(w, map[string]any{"stock": stock})
				return
			}
			n, err := strconv.ParseInt(form["quantity"], 10, 32)
			if err != nil || n < 1 {
				writeAcgErr(w, "购买数量非法")
				return
			}
			writeAcgOK(w, map[string]any{"state": stock >= n})
		case strings.HasSuffix(r.URL.Path, "/amount"):
			n, err := strconv.ParseInt(form["quantity"], 10, 32)
			pricing, priceErr := h.svc.repo.LoadPricing(ctx, account.ID)
			if err != nil || n < 1 || priceErr != nil {
				writeAcgErr(w, "购买数量或供货定价无效")
				return
			}
			price := pricing.Price(p.ID, 0, p.CategoryID, p.Price)
			if price <= 0 || price > math.MaxInt64/n {
				writeAcgErr(w, "供货金额非法")
				return
			}
			writeAcgOK(w, map[string]any{"amount": centsToYuanStr(price * n)})
		}
	}
}

func (h *mcyCompat) product(r *http.Request, p catalogport.SupplierProduct, pricing *supplierPricing, category string) (map[string]any, error) {
	stock, err := h.svc.inv.Stock(r.Context(), p.ID, 0)
	if err != nil || stock < 0 {
		return nil, errMCYStock
	}
	if category == "" {
		category = "默认分类"
	}
	return map[string]any{
		"id": p.ID, "name": p.Name, "introduce": p.Description, "picture_url": p.Cover, "widget": "[]",
		"category": map[string]any{"id": p.CategoryID, "name": category},
		"sku":      []map[string]any{{"id": p.ID, "name": p.Name, "stock_price": centsToYuanStr(pricing.Price(p.ID, 0, p.CategoryID, p.Price)), "stock": stock}},
	}, nil
}
