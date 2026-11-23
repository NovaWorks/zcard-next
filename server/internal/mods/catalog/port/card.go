package port

import "context"

const CardPurchaseCapability = "card_purchase.v1"

type cardPurchaseKey struct{}

func WithCardPurchase(ctx context.Context, v bool) context.Context {
	return context.WithValue(ctx, cardPurchaseKey{}, v)
}
func HasCardPurchase(ctx context.Context) bool { v, _ := ctx.Value(cardPurchaseKey{}).(bool); return v }

type cardPurchaseV2Key struct{}

func WithCardPurchaseV2(ctx context.Context, v bool) context.Context {
	return context.WithValue(ctx, cardPurchaseV2Key{}, v)
}
func HasCardPurchaseV2(ctx context.Context) bool {
	v, _ := ctx.Value(cardPurchaseV2Key{}).(bool)
	return v
}
