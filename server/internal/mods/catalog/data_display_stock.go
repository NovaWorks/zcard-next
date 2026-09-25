package catalog

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
)

func (r *ProductRepoImpl) SetStockLookup(lookup port.StockLookup) { r.stockLookup = lookup }

// Respond within three seconds, but let this visible page's bounded refresh
// finish in the background. Per-product deduplication prevents repeated page
// visits from multiplying the queue. No background work may retain a DB tx.
func (r *ProductRepoImpl) refreshDisplayStocks(ctx context.Context, rows []*ent.Product, snapshots map[uint64]data.ProductStockSnapshot) {
	if r.stockLookup == nil {
		return
	}
	type job struct {
		p   *ent.Product
		key string
	}
	pending := make(chan job, len(rows))
	for _, p := range rows {
		if local, e := data.HasLocalDelivery(ctx, data.Client(ctx, r.data), p); e != nil || local {
			continue
		}
		if p.UpstreamSourceID > 0 && snapshots[p.ID].Status != "current" {
			key := fmtStockKey(p)
			if _, loaded := r.stockRefreshing.LoadOrStore(key, true); !loaded {
				pending <- job{p, key}
			}
		}
	}
	close(pending)
	if len(pending) == 0 {
		return
	}
	workCtx := context.WithoutCancel(ctx)
	budget := 45 * time.Second
	if data.Client(ctx, r.data) != r.data.Client {
		workCtx = ctx
		budget = 3 * time.Second
	}
	bounded, cancel := context.WithTimeout(workCtx, budget)
	type result struct {
		id uint64
		n  int32
	}
	results := make(chan result, len(rows))
	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range pending {
				if bounded.Err() == nil {
					n, err := r.stockLookup.DisplayStock(bounded, j.p.UpstreamSourceID, j.p.UpstreamProductCode)
					if err == nil && n >= -1 {
						results <- result{j.p.ID, n}
					}
				}
				r.stockRefreshing.Delete(j.key)
			}
		}()
	}
	go func() { wg.Wait(); cancel(); close(results); close(done) }()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case res, ok := <-results:
			if !ok {
				return
			}
			snapshots[res.id] = data.ProductStockSnapshot{Quantity: int64(res.n), Status: "current", CheckedAt: time.Now().UTC()}
		case <-timer.C:
			return
		case <-ctx.Done():
			cancel()
			<-done
			return
		}
	}
}

func fmtStockKey(p *ent.Product) string {
	return fmt.Sprintf("%d/%s", p.UpstreamSourceID, p.UpstreamProductCode)
}
