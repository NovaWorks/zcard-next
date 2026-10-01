package adapter

import (
	"context"
	"fmt"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"net/url"
	"strconv"
	"time"
)

func channelQuery(f supplyport.SMSChannelFilter) url.Values {
	return url.Values{"country_id": {f.CountryID}, "platform_id": {f.PlatformID}, "keyword": {f.Keyword}, "page": {strconv.Itoa(f.Page)}, "page_size": {strconv.Itoa(f.PageSize)}}
}
func (a *zCardAdapter) SMSChannelOptions(ctx context.Context, id string, f supplyport.SMSChannelFilter) (*supplyport.SMSChannelOptions, error) {
	raw, e := a.request(ctx, "GET", "/api/supply/products/"+url.PathEscape(id)+"/sms/options", channelQuery(f), nil)
	if e != nil {
		return nil, e
	}
	var out supplyport.SMSChannelOptions
	if e = decodeZCard(raw, &out); e != nil {
		return nil, fmt.Errorf("adapter.zcard: invalid channel options")
	}
	if len(out.Countries) > 10000 || len(out.Platforms) > 10000 {
		return nil, fmt.Errorf("adapter.zcard: too many channel options")
	}
	return &out, nil
}
func (a *zCardAdapter) SMSChannelOffers(ctx context.Context, id string, f supplyport.SMSChannelFilter) (*supplyport.SMSChannelOffers, error) {
	raw, e := a.request(ctx, "GET", "/api/supply/products/"+url.PathEscape(id)+"/sms/offers", channelQuery(f), nil)
	if e != nil {
		return nil, e
	}
	var out supplyport.SMSChannelOffers
	if e = decodeZCard(raw, &out); e != nil {
		return nil, fmt.Errorf("adapter.zcard: invalid channel offers")
	}
	if len(out.Offers) > 100 {
		return nil, fmt.Errorf("adapter.zcard: unbounded channel offers")
	}
	seen := map[string]bool{}
	for _, o := range out.Offers {
		if o.OfferID == "" || seen[o.OfferID] || o.PriceCents <= 0 || o.Stock < -1 {
			return nil, fmt.Errorf("adapter.zcard: invalid channel offer")
		}
		seen[o.OfferID] = true
	}
	return &out, nil
}
func (a *zCardAdapter) SMSChannelQuote(ctx context.Context, id, offer string, f supplyport.SMSChannelFilter) (*supplyport.SMSChannelQuote, error) {
	raw, e := a.request(ctx, "POST", "/api/supply/products/"+url.PathEscape(id)+"/sms/quotes", nil, map[string]any{"offer_id": offer, "country_id": f.CountryID, "platform_id": f.PlatformID, "page": f.Page, "page_size": f.PageSize})
	if e != nil {
		return nil, e
	}
	var out supplyport.SMSChannelQuote
	if e = decodeZCard(raw, &out); e != nil || out.QuoteID == "" || len(out.QuoteID) > 64 || out.Currency != "CNY" || out.AmountCents <= 0 || int64(out.ExpiresAt) <= time.Now().Unix() {
		return nil, fmt.Errorf("adapter.zcard: invalid channel quote")
	}
	return &out, nil
}
