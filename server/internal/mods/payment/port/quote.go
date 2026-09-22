package port

import "context"

type quoteKeyContext struct{}

func WithQuoteKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, quoteKeyContext{}, key)
}
func QuoteKey(ctx context.Context) string {
	key, _ := ctx.Value(quoteKeyContext{}).(string)
	return key
}
