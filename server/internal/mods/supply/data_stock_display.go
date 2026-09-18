package supply

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"golang.org/x/sync/singleflight"
)

var displayStockRequests singleflight.Group
var displayStockSlots = make(chan struct{}, 4)

func (g *Gateway) cacheStock(ctx context.Context, connectionID uint64, code string, n int32, queryErr error) {
	g.cacheStockAt(ctx, connectionID, code, n, queryErr, time.Now().UTC())
}

func (g *Gateway) cacheStockAt(ctx context.Context, connectionID uint64, code string, n int32, queryErr error, started time.Time) {
	if queryErr != nil || n < -1 {
		n = -2
	}
	// A timed-out upstream context must still record its failed attempt.
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := g.repo.recordStock(bounded, connectionID, code, "", n, started); err != nil {
		slog.WarnContext(ctx, "supply.stock.cache_failed", "connection_id", connectionID, "error", err)
	}
	if queryErr != nil {
		slog.WarnContext(ctx, "supply.stock.query_failed", "connection_id", connectionID, "product_code", code, "reason", adapter.StockErrorSummary(queryErr))
	}
}

func (g *Gateway) DisplayStock(ctx context.Context, connectionID uint64, code string) (int32, error) {
	// Coalesce per repository/connection/product, with a bounded upstream request.
	key := fmt.Sprintf("%p/%d/%s", g.repo, connectionID, code)
	result := displayStockRequests.DoChan(key, func() (any, error) {
		bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
		defer cancel()
		m, err := data.Client(bounded, g.repo.data).SupplyMapping.Query().Where(supplymapping.ConnectionID(connectionID), supplymapping.UpstreamProduct(code), supplymapping.UpstreamSkuEQ("")).Only(bounded)
		if err == nil && !m.StockCheckedAt.IsZero() && time.Since(m.StockCheckedAt) < 15*time.Second {
			return m.UpStock, nil
		}
		select {
		case displayStockSlots <- struct{}{}:
			defer func() { <-displayStockSlots }()
		case <-bounded.Done():
			return int32(-2), bounded.Err()
		}
		return g.CheckStock(bounded, connectionID, code, "")
	})
	select {
	case <-ctx.Done():
		return -2, ctx.Err()
	case r := <-result:
		if r.Err != nil {
			return -2, r.Err
		}
		return r.Val.(int32), nil
	}
}
