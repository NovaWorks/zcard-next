package adapter

import "context"

type acgCatalogStockKey struct{}

// WithACGCatalogStock only changes maintenance catalog reads. Direct stock and
// purchase requests continue to verify the requested product/SKU upstream.
func WithACGCatalogStock(ctx context.Context) context.Context {
	return context.WithValue(ctx, acgCatalogStockKey{}, true)
}

func preferACGCatalogStock(ctx context.Context) bool {
	value, _ := ctx.Value(acgCatalogStockKey{}).(bool)
	return value
}
