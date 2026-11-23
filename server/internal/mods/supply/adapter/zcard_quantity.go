package adapter

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// CheckQuantity is an authenticated, read-only availability check. Unknown
// inventory remains unknown; this result only applies to the requested amount.
func (a *zCardAdapter) CheckQuantity(ctx context.Context, code, sku string, quantity int) (bool, error) {
	if quantity < 1 {
		return false, ErrProductUnavailable
	}
	q := url.Values{"quantity": {strconv.Itoa(quantity)}, "capabilities": {"card_purchase.v1", "card_purchase.v2"}}
	if sku != "" {
		q.Set("sku_id", sku)
	}
	raw, err := a.request(stockReadContext(ctx), "GET", "/api/supply/products/"+url.PathEscape(code)+"/availability", q, nil)
	if err != nil {
		return false, err
	}
	var v struct {
		Available *bool `json:"available"`
		Quantity  int   `json:"quantity"`
	}
	if err = decodeZCard(raw, &v); err != nil || v.Available == nil || v.Quantity != quantity {
		return false, fmt.Errorf("上游购买数量确认响应无效")
	}
	return *v.Available, nil
}
