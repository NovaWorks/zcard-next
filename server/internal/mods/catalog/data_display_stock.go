package catalog

import (
	"context"
	"sync"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
)

// SetStockLookup is wired once at startup, after the supply/catalog dependency cycle is resolved.
func (r *ProductRepoImpl) SetStockLookup(lookup port.StockLookup) { r.stockLookup = lookup }

// Refresh only the requested page's stale upstream snapshots. A slow or unavailable
// source must not block the catalog indefinitely or turn old counts into live stock.
func (r *ProductRepoImpl) refreshDisplayStocks(ctx context.Context, rows []*ent.Product, snapshots map[uint64]data.ProductStockSnapshot) {
	if r.stockLookup == nil {
		return
	}
	pending := make(chan *ent.Product, len(rows))
	for _, p := range rows {
		if p.UpstreamSourceID > 0 && snapshots[p.ID].Status != "current" {
			pending <- p
		}
	}
	close(pending)
	if len(pending) == 0 {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range pending {
				if bounded.Err() != nil {
					return
				}
				n, err := r.stockLookup.DisplayStock(bounded, p.UpstreamSourceID, p.UpstreamProductCode)
				if err != nil || n < -1 {
					continue
				}
				mu.Lock()
				snapshots[p.ID] = data.ProductStockSnapshot{Quantity: int64(n), Status: "current", CheckedAt: time.Now().UTC()}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}
