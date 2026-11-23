package order

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	kerrors "github.com/go-kratos/kratos/v3/errors"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

// Card choices use the same public product visibility and buyer restrictions as
// checkout. No provider credentials, original IDs or card secrets are returned.
func RegisterCardHTTPServer(srv *khttp.Server, s *StoreOrderService) {
	srv.Route("/").POST("/api/v1/storefront/products/{id}/card/drafts", func(c khttp.Context) error {
		c.Response().Header().Set("Cache-Control", "no-store, private")
		khttp.SetOperation(c, storefrontv1.OperationStoreOrderServiceQuoteOrder)
		h := c.Middleware(func(ctx context.Context, _ any) (any, error) {
			path := strings.Split(strings.Trim(c.Request().URL.Path, "/"), "/")
			if len(path) != 7 {
				return nil, kerrors.BadRequest("order.FORM_INVALID", "商品无效")
			}
			id, e := strconv.ParseUint(path[4], 10, 64)
			if e != nil || id == 0 {
				return nil, kerrors.BadRequest("order.FORM_INVALID", "商品无效")
			}
			var in struct {
				SKUID json.Number `json:"sku_id"`
				Page  int         `json:"page"`
				Limit int         `json:"limit"`
			}
			d := json.NewDecoder(io.LimitReader(c.Request().Body, 8192))
			d.DisallowUnknownFields()
			if d.Decode(&in) != nil {
				return nil, kerrors.BadRequest("order.FORM_INVALID", "请求无效")
			}
			var extra any
			if d.Decode(&extra) != io.EOF {
				return nil, kerrors.BadRequest("order.FORM_INVALID", "请求无效")
			}
			sku := uint64(0)
			if in.SKUID != "" {
				sku, e = strconv.ParseUint(string(in.SKUID), 10, 64)
				if e != nil {
					return nil, kerrors.BadRequest("order.FORM_INVALID", "规格无效")
				}
			}
			if in.Page == 0 {
				in.Page = 1
			}
			if in.Limit == 0 {
				in.Limit = 10
			}
			subsite := tenancy.FromContext(ctx).SubsiteID
			p, e := data.ProductForDelivery(ctx, data.Client(ctx, s.uc.Data), subsite, id)
			if e != nil || p.Status != 1 {
				return nil, kerrors.NotFound("order.PRODUCT_NOT_FOUND", "商品不可用")
			}
			hidden, e := data.HiddenCategoryIDs(ctx, data.Client(ctx, s.uc.Data), subsite)
			if e != nil {
				return nil, e
			}
			if data.CategoryHidden(hidden, p.CategoryID) {
				return nil, kerrors.NotFound("order.PRODUCT_NOT_FOUND", "商品不可用")
			}
			// Reader applies hidden-category/member visibility rules.
			if s.uc.Catalog == nil {
				return nil, kerrors.ServiceUnavailable("order.QUOTE_UNAVAILABLE", "商品服务暂不可用")
			}
			if _, e = s.uc.Catalog.ResolvePrice(ctx, p.ID, sku); e != nil {
				return nil, mapOrderErr(e)
			}
			selected, e := data.DeliverySKU(ctx, data.Client(ctx, s.uc.Data), p, sku)
			if e != nil {
				return nil, mapOrderErr(e)
			}
			upstreamSKU := ""
			if selected != nil {
				upstreamSKU = selected.UpstreamSkuID
			}
			g, ok := s.uc.StockGate.(supplyport.CardPurchaseGateway)
			if !ok {
				return nil, kerrors.ServiceUnavailable("order.QUOTE_UNAVAILABLE", "预选卡暂不可用")
			}
			out, e := g.ListCardPicks(ctx, supplyport.CardPurchaseRequest{ProductID: p.ID, SKUID: sku, ConnectionID: p.UpstreamSourceID, ProductCode: p.UpstreamProductCode, UpstreamSKU: upstreamSKU}, in.Page, in.Limit)
			if e != nil {
				return nil, kerrors.BadRequest("order.FORM_INVALID", e.Error())
			}
			return out, nil
		})
		out, e := h(c, nil)
		if e != nil {
			return e
		}
		return c.Result(http.StatusOK, out)
	})
}
