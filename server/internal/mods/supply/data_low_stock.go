package supply

import (
	"context"
	dbsql "database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

type lowStockPlan struct {
	Enabled   bool `json:"enabled"`
	Threshold int  `json:"threshold"`
	Interval  int  `json:"interval"`
}

func loadLowStockPlan(conn *ent.SupplyConnection) lowStockPlan {
	p := lowStockPlan{Threshold: 10, Interval: 5}
	sched, _ := conn.Settings["schedule"].(map[string]any)
	if raw, ok := sched["low_stock"]; ok {
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &p)
	}
	if p.Threshold < 1 || p.Threshold > 1000000 || p.Interval < 5 || p.Interval > 1440 {
		p.Enabled = false
	}
	return p
}
func validateLowStockPlan(settings map[string]any) error {
	sched, _ := settings["schedule"].(map[string]any)
	raw, ok := sched["low_stock"]
	if !ok {
		return nil
	}
	var p lowStockPlan
	b, _ := json.Marshal(raw)
	if string(b) == "null" || json.Unmarshal(b, &p) != nil || p.Threshold < 1 || p.Threshold > 1000000 || p.Interval < 5 || p.Interval > 1440 {
		return fmt.Errorf("低库存加速设置无效：数量须为 1–1000000，间隔须为 5–1440 分钟的整数")
	}
	return nil
}
func monitorEligible(p *ent.Product) bool {
	return !p.IsLocked && p.SubsiteID == 0 && (p.Status > 0 || p.Status == 0 && p.AutoListing && p.ListingReason == "stock_out")
}

// These probes never acquire the catalog-task lease or change catalog metadata.
// Each mapping owns a persisted probe lease; writes recheck the product guard,
// route and connection fingerprint. Thus long imports cannot starve stock reads.
func (s *SyncService) ScanLowStock(ctx context.Context) {
	conns, err := s.repo.ListActiveConnections(ctx)
	if err != nil {
		s.log.Warn("stock.monitor.list", "err", err)
		return
	}
	// Persisted rotation prevents a busy first supplier from consuming every cron budget.
	sort.SliceStable(conns, func(i, j int) bool { return conns[i].LowStockScannedAt < conns[j].LowStockScannedAt })
	for _, conn := range conns {
		if ctx.Err() != nil {
			return
		}
		plan := loadLowStockPlan(conn)
		if !plan.Enabled {
			continue
		}
		if !conn.RateLimitUntil.IsZero() && time.Now().Before(conn.RateLimitUntil) {
			s.monitorProgress(ctx, conn.ID, "上游限流冷却中，预计 "+conn.RateLimitUntil.Local().Format(time.RFC3339)+" 后重试")
			continue
		}
		// A per-connection bounded round lets the following connection make progress.
		call, stop := context.WithTimeout(ctx, 12*time.Second)
		err = s.ensureStockSlots(call, conn)
		if err == nil {
			err = s.probeLowStock(call, conn, plan)
		}
		if err != nil {
			s.monitorProgress(ctx, conn.ID, "本轮未完成，将继续检查："+adapter.StockErrorSummary(err))
		}
		stop()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			s.log.Warn("stock.monitor.scan", "connection_id", conn.ID, "err", err)
		}
	}
}
func (s *SyncService) ensureStockSlots(ctx context.Context, conn *ent.SupplyConnection) error {
	c := s.repo.entClient(ctx)
	ps, err := c.Product.Query().Where(product.SubsiteID(0), product.UpstreamSourceID(conn.ID), product.IsLocked(false), product.Or(product.StatusGT(0), product.And(product.Status(0), product.AutoListing(true), product.ListingReason("stock_out"))), func(outer *entsql.Selector) {
		sk := entsql.Table(productsku.Table)
		m := entsql.Table(supplymapping.Table)
		mappings := entsql.Select(m.C(supplymapping.FieldID)).From(m).Where(entsql.And(
			entsql.EQ(m.C(supplymapping.FieldConnectionID), conn.ID),
			entsql.ColumnsEQ(m.C(supplymapping.FieldLocalProductID), outer.C(product.FieldID)),
			entsql.ColumnsEQ(m.C(supplymapping.FieldUpstreamProduct), outer.C(product.FieldUpstreamProductCode)),
		))
		skus := entsql.Select(sk.C(productsku.FieldID)).From(sk).Where(entsql.ColumnsEQ(sk.C(productsku.FieldProductID), outer.C(product.FieldID)))
		missingSKU := skus.Clone().Where(entsql.And(
			entsql.NEQ(sk.C(productsku.FieldUpstreamSkuID), ""),
			entsql.Or(entsql.NotIn(sk.C(productsku.FieldFulfillmentMode), "local", "reuse", "follow", ""),
				entsql.And(entsql.In(sk.C(productsku.FieldFulfillmentMode), "follow", ""), entsql.NotIn(outer.C(product.FieldFulfillmentMode), "local", "reuse"))),
			entsql.Not(entsql.Exists(mappings.Clone().Where(entsql.ColumnsEQ(m.C(supplymapping.FieldUpstreamSku), sk.C(productsku.FieldUpstreamSkuID))))),
		))
		outer.Where(entsql.Or(entsql.Exists(missingSKU), entsql.And(
			entsql.Not(entsql.Exists(skus)), entsql.NotIn(outer.C(product.FieldFulfillmentMode), "local", "reuse"),
			entsql.Not(entsql.Exists(mappings.Clone().Where(entsql.EQ(m.C(supplymapping.FieldUpstreamSku), "")))),
		)))
	}).Order(ent.Asc(product.FieldID)).Limit(100).All(ctx)
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		return nil
	}
	for _, p := range ps {
		if !monitorEligible(p) {
			continue
		}
		slots, err := data.StockSlots(ctx, s.repo.data, p)
		if err != nil {
			return err
		}
		for _, slot := range slots {
			if slot.Source != "upstream" || slot.Mapping != nil || slot.SKUID > 0 && slot.UpstreamSKU == "" {
				continue
			}
			err = data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
				current, e := data.GuardProductWrite(ctx, s.repo.data, p.ID)
				if e != nil {
					return e
				}
				if current.LockVersion != p.LockVersion || !monitorEligible(current) {
					return nil
				}
				e = s.repo.entClient(ctx).SupplyMapping.Create().SetConnectionID(conn.ID).SetLocalProductID(p.ID).SetLocalSkuID(slot.SKUID).SetUpstreamProduct(p.UpstreamProductCode).SetUpstreamSku(slot.UpstreamSKU).SetUpStock(-2).OnConflict(entsql.ConflictColumns(supplymapping.FieldConnectionID, supplymapping.FieldUpstreamProduct, supplymapping.FieldUpstreamSku)).DoNothing().Exec(ctx)
				if errors.Is(e, dbsql.ErrNoRows) {
					return nil
				}
				return e
			})
			if err != nil && !data.IsProductLocked(err) {
				return err
			}
		}
	}
	// Remaining missing mappings are discovered next round; known low stock gets time now.
	return nil
}
func (s *SyncService) probeLowStock(ctx context.Context, conn *ent.SupplyConnection, plan lowStockPlan) error {
	c := s.repo.entClient(ctx)
	now := time.Now().UTC()
	// Successful observations from any path postpone the next probe. Failures use
	// a separate retry deadline and never replace the last successful reference.
	due := supplymapping.And(supplymapping.ConnectionID(conn.ID), supplymapping.StockProbeLeaseLTE(now.Unix()), supplymapping.StockProbeAfterLTE(now.Unix()),
		supplymapping.Or(supplymapping.UpStockLT(-1), supplymapping.StockCheckedAtIsNil(), supplymapping.And(supplymapping.UpStockGTE(0), supplymapping.UpStockLTE(int32(plan.Threshold)))))
	due = supplymapping.And(due, func(outer *entsql.Selector) {
		p := entsql.Table(product.Table)
		sk := entsql.Table(productsku.Table)
		productMatch := entsql.Select(p.C(product.FieldID)).From(p).Where(entsql.And(
			entsql.ColumnsEQ(p.C(product.FieldID), outer.C(supplymapping.FieldLocalProductID)),
			entsql.EQ(p.C(product.FieldSubsiteID), 0), entsql.EQ(p.C(product.FieldIsLocked), false),
			entsql.EQ(p.C(product.FieldUpstreamSourceID), conn.ID),
			entsql.ColumnsEQ(p.C(product.FieldUpstreamProductCode), outer.C(supplymapping.FieldUpstreamProduct)),
			entsql.Or(entsql.GT(p.C(product.FieldStatus), 0), entsql.And(entsql.EQ(p.C(product.FieldStatus), 0), entsql.EQ(p.C(product.FieldAutoListing), true), entsql.EQ(p.C(product.FieldListingReason), "stock_out"))),
		))
		allSKUs := entsql.Select(sk.C(productsku.FieldID)).From(sk).Where(entsql.ColumnsEQ(sk.C(productsku.FieldProductID), outer.C(supplymapping.FieldLocalProductID)))
		upstreamSKU := allSKUs.Clone().Where(entsql.And(
			entsql.ColumnsEQ(sk.C(productsku.FieldUpstreamSkuID), outer.C(supplymapping.FieldUpstreamSku)),
			entsql.Or(
				entsql.NotIn(sk.C(productsku.FieldFulfillmentMode), "local", "reuse", "follow", ""),
				entsql.And(entsql.In(sk.C(productsku.FieldFulfillmentMode), "follow", ""), entsql.NotIn(p.C(product.FieldFulfillmentMode), "local", "reuse")),
			),
		))
		productMatch.Where(entsql.Or(
			entsql.And(entsql.EQ(outer.C(supplymapping.FieldUpstreamSku), ""), entsql.Not(entsql.Exists(allSKUs)), entsql.NotIn(p.C(product.FieldFulfillmentMode), "local", "reuse")),
			entsql.And(entsql.NEQ(outer.C(supplymapping.FieldUpstreamSku), ""), entsql.Exists(upstreamSKU)),
		))
		outer.Where(entsql.Exists(productMatch))
	})
	rows, err := c.SupplyMapping.Query().Where(due).Order(func(q *entsql.Selector) {
		q.OrderExpr(entsql.ExprFunc(func(b *entsql.Builder) {
			b.WriteString("CASE WHEN ").Ident(q.C(supplymapping.FieldUpStock)).WriteString(" >= 0 THEN 0 ELSE 1 END")
		}))
	}, ent.Asc(supplymapping.FieldStockProbeAfter), ent.Asc(supplymapping.FieldStockCheckedAt), ent.Asc(supplymapping.FieldID)).Limit(30).All(ctx)
	if err != nil {
		return err
	}
	checked, failed := 0, 0
	defer func() {
		if ctx.Err() == nil {
			message := fmt.Sprintf("本轮检查 %d 个规格，%d 个待确认；按各规格成功时间到期检查，失败自动退避", checked, failed)
			s.monitorProgress(ctx, conn.ID, message)
		}
	}()
	for _, m := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p, e := c.Product.Get(ctx, m.LocalProductID)
		if e != nil {
			if ent.IsNotFound(e) {
				continue
			}
			return e
		}
		if !monitorEligible(p) || p.UpstreamSourceID != conn.ID || p.UpstreamProductCode != m.UpstreamProduct {
			continue
		}
		slots, e := data.StockSlots(ctx, s.repo.data, p)
		if e != nil {
			return e
		}
		var target *data.StockSlot
		for i := range slots {
			if slots[i].Source == "upstream" && slots[i].Mapping != nil && slots[i].Mapping.ID == m.ID {
				target = &slots[i]
				break
			}
		}
		if target == nil {
			continue
		}
		if !m.StockCheckedAt.IsZero() && time.Since(m.StockCheckedAt) < time.Duration(plan.Interval)*time.Minute {
			_ = c.SupplyMapping.Update().Where(supplymapping.ID(m.ID), supplymapping.StockProbeLeaseLTE(now.Unix())).SetStockProbeAfter(m.StockCheckedAt.Add(time.Duration(plan.Interval) * time.Minute).Unix()).Exec(ctx)
			continue
		}
		lease := time.Now().Unix() + 30
		n, e := c.SupplyMapping.Update().Where(supplymapping.ID(m.ID), due).SetStockProbeLease(lease).Save(ctx)
		if e != nil {
			return e
		}
		if n == 0 {
			continue
		}
		started := time.Now().UTC()
		a, e := s.lowStockAdapter(conn)
		stock := int32(-2)
		if e == nil {
			call, cancel := context.WithTimeout(ctx, 6*time.Second)
			stock, e = a.GetStock(call, m.UpstreamProduct, m.UpstreamSku)
			cancel()
		}
		queryErr := e
		checked++
		if queryErr != nil || stock < -1 {
			failed++
		}
		if queryErr != nil || stock < -1 {
			stock = -2
		}
		// Use an independent short context to release and record timed-out attempts.
		save, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		e = data.Tx(save, s.repo.data, func(tx context.Context) error {
			current, err := data.GuardProductWrite(tx, s.repo.data, p.ID)
			if err != nil {
				return err
			}
			freshConn, err := s.repo.GetConnection(tx, conn.ID)
			if err != nil {
				return err
			}
			if current.LockVersion != p.LockVersion || !monitorEligible(current) || data.ConnectionRevision(freshConn) != data.ConnectionRevision(conn) || !loadLowStockPlan(freshConn).Enabled {
				return nil
			}
			freshSlots, err := data.StockSlots(tx, s.repo.data, current)
			if err != nil {
				return err
			}
			valid := false
			for _, slot := range freshSlots {
				if slot.SKUID == target.SKUID && slot.SourceKey == target.SourceKey && slot.Mapping != nil && slot.Mapping.ID == m.ID {
					valid = true
				}
			}
			if !valid {
				return nil
			}
			row, err := s.repo.entClient(tx).SupplyMapping.Get(tx, m.ID)
			if err != nil {
				return err
			}
			if row.StockProbeLease != lease || (!row.StockCheckedAt.IsZero() && started.Before(row.StockCheckedAt)) {
				return nil
			}
			if err = s.repo.recordStock(tx, conn.ID, m.UpstreamProduct, m.UpstreamSku, stock, started); err != nil {
				return err
			}
			upd := s.repo.entClient(tx).SupplyMapping.UpdateOneID(m.ID).SetStockProbeLease(0)
			if stock < -1 {
				upd.SetStockProbeFailures(m.StockProbeFailures + 1).SetStockProbeAfter(started.Add(time.Duration(min(60, plan.Interval*(1<<min(m.StockProbeFailures, 4)))) * time.Minute).Unix())
			} else {
				upd.SetStockProbeFailures(0).SetStockProbeAfter(started.Add(time.Duration(plan.Interval) * time.Minute).Unix())
			}
			if err = upd.Exec(tx); err != nil {
				return err
			}
			return s.observeMonitoredStock(tx, current, started)
		})
		// Conditional release cannot clear a newer owner's lease.
		_, releaseErr := c.SupplyMapping.Update().Where(supplymapping.ID(m.ID), supplymapping.StockProbeLease(lease)).SetStockProbeLease(0).Save(save)
		stop()
		if e != nil && !data.IsProductLocked(e) {
			return e
		}
		if releaseErr != nil {
			return releaseErr
		}
		if errors.Is(queryErr, adapter.ErrRateLimited) {
			if s.pacer != nil {
				s.pacer.OnRateLimited(context.WithoutCancel(ctx), conn, "低库存检查限流")
			}
			return nil
		}
		delay := max(loadScheduleSettings(conn).StockBatchDelay, 200*time.Millisecond)
		if s.pacer != nil {
			delay = max(delay, s.pacer.Delay(conn))
		}
		if err = sleepCtx(ctx, delay); err != nil {
			return err
		}
	}
	return nil
}
func (s *SyncService) observeMonitoredStock(ctx context.Context, p *ent.Product, at time.Time) error {
	slots, err := data.StockSlots(ctx, s.repo.data, p)
	if err != nil {
		return err
	}
	total := int64(0)
	unknown := false
	oldest := at
	for _, slot := range slots {
		if slot.CheckedAt.Before(oldest) {
			oldest = slot.CheckedAt
		}
		if slot.Source != "upstream" {
			return nil
		}
		if slot.Quantity == -1 {
			total = -1
			continue
		}
		if slot.Quantity < 0 {
			unknown = true
		} else if total != -1 {
			total += slot.Quantity
		}
	}
	if len(slots) > 0 && slots[0].SKUID > 0 && !unknown && !oldest.IsZero() {
		if err := s.repo.recordStock(ctx, p.UpstreamSourceID, p.UpstreamProductCode, "", int32(min(total, 2147483647)), oldest); err != nil && !errors.Is(err, ErrNotFound) && !ent.IsNotFound(err) {
			return err
		}
	}
	if total == 0 && unknown {
		total = -2
	}
	return data.ObserveListing(ctx, s.repo.data, p, int32(min(total, 2147483647)), true, at)
}

// Record SKU reads from normal status synchronization and order preflight too.
func (r *SupplyRepoImpl) recordSKUStock(ctx context.Context, conn uint64, code, sku string, n int32, at time.Time) error {
	if sku == "" {
		return r.recordStock(ctx, conn, code, sku, n, at)
	}
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		c := r.entClient(ctx)
		p, err := c.Product.Query().Where(product.UpstreamSourceID(conn), product.UpstreamProductCode(code), product.SubsiteID(0), product.StatusGTE(0)).Only(ctx)
		if err != nil {
			return err
		}
		p, err = data.GuardProductWrite(ctx, r.data, p.ID)
		if err != nil {
			return err
		}
		sk, err := c.ProductSku.Query().Where(productsku.ProductID(p.ID), productsku.UpstreamSkuID(sku)).Only(ctx)
		if err != nil {
			return err
		}
		if data.StockSource(p, sk) != "upstream" {
			return nil
		}
		err = c.SupplyMapping.Create().SetConnectionID(conn).SetUpstreamProduct(code).SetUpstreamSku(sku).SetLocalProductID(p.ID).SetLocalSkuID(sk.ID).SetUpStock(-2).OnConflict(entsql.ConflictColumns(supplymapping.FieldConnectionID, supplymapping.FieldUpstreamProduct, supplymapping.FieldUpstreamSku)).DoNothing().Exec(ctx)
		if err != nil && !errors.Is(err, dbsql.ErrNoRows) {
			return err
		}
		return r.recordStock(ctx, conn, code, sku, n, at)
	})
}

func (s *SyncService) lowStockAdapter(conn *ent.SupplyConnection) (adapter.Adapter, error) {
	if s.stockAdapterFactory != nil {
		return s.stockAdapterFactory(conn)
	}
	raw, err := s.repo.OpenCredentials(conn)
	if err != nil {
		return nil, err
	}
	var creds adapter.Credentials
	if err = json.Unmarshal([]byte(raw), &creds); err != nil {
		return nil, err
	}
	return adapter.New(conn.Driver, conn.BaseURL, creds, parseRetryIntervals(conn.RetryIntervals))
}

func (s *SyncService) monitorProgress(ctx context.Context, id uint64, message string) {
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := s.repo.entClient(bounded).SupplyConnection.UpdateOneID(id).SetLowStockScannedAt(time.Now().Unix()).SetLowStockMessage(message).Exec(bounded); err != nil {
		s.log.Warn("stock.monitor.progress", "err", err)
	}
}
