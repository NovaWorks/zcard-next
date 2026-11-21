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
	if sku != "" || quantity < 1 {
		return false, ErrProductUnavailable
	}
	raw, err := a.request(stockReadContext(ctx), "GET", "/api/supply/products/"+url.PathEscape(code)+"/availability", url.Values{"quantity": {strconv.Itoa(quantity)}}, nil)
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
