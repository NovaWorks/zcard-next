package adapter

import "context"

// CardQuoteRequest describes the complete purchase, never a reference unit price.
type CardQuoteRequest struct {
	ProductCode    string
	UpstreamSKU    string
	Quantity       int
	CardID         string
	ManualFormData map[string]any
}

type CardQuote struct {
	TotalCents            int64
	SelectionPremiumCents int64
	QuoteID               string
	ExpiresAt             int64
	Currency              string
}

type CardQuoter interface {
	QuoteCard(context.Context, CardQuoteRequest) (*CardQuote, error)
}

type CardPick struct {
	ID           string
	Preview      string
	PremiumCents int64
}

type CardPickList struct {
	Items []CardPick
	Total int
}

type CardPicker interface {
	ListCardPicks(ctx context.Context, code, sku string, page, limit int) (*CardPickList, error)
	GetCardPick(ctx context.Context, code, sku, id string) (*CardPick, error)
}
