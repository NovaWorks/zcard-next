package supply

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplysynctask"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

// Stock-only recovery reads existing mappings, not the upstream catalog; it
// cannot recreate deleted products or alter prices, categories, or listing state.
func (s *SyncService) runStockOnly(ctx context.Context, task *ent.SupplySyncTask, conn *ent.SupplyConnection, a adapter.Adapter, cfg scheduleSettings) error {
	failed, total := 0, 0
	var samples []string
	var lastID uint64
	client := s.repo.entClient(ctx)
	for {
		q := client.SupplyMapping.Query().Where(supplymapping.ConnectionID(conn.ID), supplymapping.UpstreamSkuEQ(""), supplymapping.IDGT(lastID)).Order(ent.Asc(supplymapping.FieldID)).Limit(100)
		if task.Mode == "failed" {
			q.Where(supplymapping.Or(supplymapping.UpStockLT(-1), supplymapping.StockCheckedAtIsNil()))
		}
		rows, err := q.All(ctx)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		items := make([]adapter.Product, 0, len(rows))
		for _, m := range rows {
			lastID = m.ID
			exists, err := client.Product.Query().Where(product.ID(m.LocalProductID), product.UpstreamSourceID(conn.ID), product.UpstreamProductCode(m.UpstreamProduct), product.StatusGTE(0)).Exist(ctx)
			if err != nil {
				return err
			}
			if !exists {
				continue
			}
			items = append(items, adapter.Product{ID: m.UpstreamProduct, Stock: -2})
		}
		if err := s.backfillStocks(ctx, a, cfg, items, task.ID); err != nil {
			if errors.Is(err, errStockCanceled) {
				_ = s.repo.FinishTask(ctx, task.ID, supplysynctask.StatusCanceled, "", "")
				s.publishCompleted(ctx, conn.ID, task.ID, "canceled")
				return nil
			}
			_ = s.repo.FinishTask(ctx, task.ID, supplysynctask.StatusFailed, "STOCK_QUERY_FAILED", adapter.StockErrorSummary(err))
			s.publishCompleted(ctx, conn.ID, task.ID, "failed")
			return nil
		}
		for _, p := range items {
			if err := s.repo.recordStock(ctx, conn.ID, p.ID, "", p.Stock, p.StockCheckedAt); err != nil {
				return err
			}
			total++
			if p.Stock < -1 {
				failed++
				if len(samples) < 10 {
					samples = append(samples, fmt.Sprintf("%s: %s", p.ID, p.StockError))
				}
			}
		}
		canceled, err := s.repo.TouchTask(ctx, task.ID, TaskProgress{Stage: "fetching_stock", Processed: int32(len(items)), Updated: int32(len(items))})
		if err != nil {
			return err
		}
		if canceled {
			_ = s.repo.FinishTask(ctx, task.ID, supplysynctask.StatusCanceled, "", "")
			s.publishCompleted(ctx, conn.ID, task.ID, "canceled")
			return nil
		}
	}
	status, code := supplysynctask.StatusDone, ""
	summary := fmt.Sprintf("库存成功 %d，失败 %d。%s", total-failed, failed, strings.Join(samples, "；"))
	if failed > 0 {
		status = supplysynctask.StatusFailed
		code = "STOCK_QUERY_FAILED"
	}
	_ = s.repo.FinishTask(ctx, task.ID, status, code, summary)
	s.publishCompleted(ctx, conn.ID, task.ID, string(status))
	return nil
}
