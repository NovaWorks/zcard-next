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
	country, e := parseChannelQuoteID("country_id", f.CountryID)
	if e != nil {
		return nil, e
	}
	platform, e := parseChannelQuoteID("platform_id", f.PlatformID)
	if e != nil {
		return nil, e
	}
	// Supply quote filters are JSON integers; storefront IDs remain strings.
	body := struct {
		OfferID    string `json:"offer_id"`
		CountryID  int64  `json:"country_id"`
		PlatformID int64  `json:"platform_id"`
		Page       int    `json:"page"`
		PageSize   int    `json:"page_size"`
	}{OfferID: offer, CountryID: country, PlatformID: platform, Page: f.Page, PageSize: f.PageSize}
	raw, e := a.request(ctx, "POST", "/api/supply/products/"+url.PathEscape(id)+"/sms/quotes", nil, body)
	if e != nil {
		return nil, e
	}
	var out supplyport.SMSChannelQuote
	if e = decodeZCard(raw, &out); e != nil || out.QuoteID == "" || len(out.QuoteID) > 64 || out.Currency != "CNY" || out.AmountCents <= 0 || int64(out.ExpiresAt) <= time.Now().Unix() {
		return nil, fmt.Errorf("adapter.zcard: invalid channel quote")
	}
	return &out, nil
}

func parseChannelQuoteID(field, value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("adapter.zcard: invalid channel %s", field)
	}
	return id, nil
}
