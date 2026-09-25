package adapter

import (
	"context"
	"time"
)

type catalogReadKey struct{}

// WithCatalogRead is only for read-only catalog jobs. Purchase requests retain
// their existing timeout/retry policy. Never apply this to order submission.
func WithCatalogRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, catalogReadKey{}, true)
}
func isCatalogRead(ctx context.Context) bool { v, _ := ctx.Value(catalogReadKey{}).(bool); return v }

const catalogRequestTimeout = 120 * time.Second
