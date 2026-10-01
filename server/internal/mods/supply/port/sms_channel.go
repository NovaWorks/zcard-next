package port

import "context"

const SMSProductKind = "sms_channel"
const SMSProductCatalog = "sms_channel_catalog.v1"
const SMSProductPurchase = "sms_channel_purchase.v1"

type SMSChannelOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type SMSChannelOptions struct {
	Countries []SMSChannelOption `json:"countries"`
	Platforms []SMSChannelOption `json:"platforms"`
}
type SMSChannelOffer struct {
	OfferID    string  `json:"offer_id"`
	Name       string  `json:"name"`
	PriceCents Integer `json:"price_cents"`
	Stock      int32   `json:"stock"`
}
type SMSChannelOffers struct {
	Offers  []SMSChannelOffer `json:"offers"`
	HasMore bool              `json:"has_more"`
}
type SMSChannelQuote struct {
	QuoteID     string  `json:"quote_id"`
	AmountCents Integer `json:"amount_cents"`
	Currency    string  `json:"currency"`
	ExpiresAt   Integer `json:"expires_at"`
	OfferName   string  `json:"offer_name"`
}
type SMSChannelFilter struct {
	CountryID  string
	PlatformID string
	Keyword    string
	Page       int
	PageSize   int
}
type SMSChannelClient interface {
	SMSChannelOptions(context.Context, string, SMSChannelFilter) (*SMSChannelOptions, error)
	SMSChannelOffers(context.Context, string, SMSChannelFilter) (*SMSChannelOffers, error)
	SMSChannelQuote(context.Context, string, string, SMSChannelFilter) (*SMSChannelQuote, error)
}
