package port

import "context"

// SocialDisplayPrice is a retail sample total for a stated quantity, never a quote.
type SocialDisplayPrice struct {
	AmountCents int64  `json:"amount_cents"`
	Quantity    int64  `json:"quantity"`
	Currency    string `json:"currency"`
}

type SocialDisplayPriceReader interface {
	DisplaySocialPrices(context.Context, []Product) map[uint64]SocialDisplayPrice
}

type socialCapabilityKey struct{}

func WithSocialCapability(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, socialCapabilityKey{}, enabled)
}

func HasSocialCapability(ctx context.Context) bool {
	enabled, _ := ctx.Value(socialCapabilityKey{}).(bool)
	return enabled
}
