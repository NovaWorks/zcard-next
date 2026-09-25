package supply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyimportitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplysynctask"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

const importMaxAttempts = 3

var errImportConfiguration = errors.New("货源配置已变化，请重新选择未完成商品并确认定价")

func (s *SyncService) runImportTask(ctx context.Context, task *ent.SupplySyncTask) error {
	payload, err := readImportPayload(ctx, task, false)
	if err != nil {
		return err
	}
	ctx = tenancy.WithContext(ctx, tenancy.Context{SubsiteID: payload.Tenant, IsMain: payload.Tenant == 0})
	if !task.CancelRequestedAt.IsZero() {
		return s.finishImport(ctx, task.ID, supplysynctask.StatusCanceled, "", "已停止，已导入商品保留")
	}
	conn, a, err := NewAdminSupplyService(s.repo, s).adapterForConnection(ctx, task.ConnectionID)
	if err != nil {
		return s.pauseImport(ctx, task.ID, "CONNECTION_INVALID", "无法读取货源配置，请检查后继续")
	}
	if string(conn.Status) != "active" {
		return s.pauseImport(ctx, task.ID, "CONNECTION_DISABLED", "货源已停用，请启用后继续任务")
	}
	if payload.Fingerprint != importFingerprint(conn) {
		return s.pauseImport(ctx, task.ID, "CONFIG_CHANGED", errImportConfiguration.Error())
	}
	if err := s.repo.SetTaskProcessing(ctx, task.ID, task.TotalCount); err != nil {
		return err
	}
	return s.executeImportItems(ctx, task, payload, conn, a)
}

// Each upstream operation is bounded independently. The HTTP request which
// created the task is long gone; its deadline never becomes an item failure.
func (s *SyncService) executeImportItems(ctx context.Context, task *ent.SupplySyncTask, payload *importPayload, conn *ent.SupplyConnection, a adapter.Adapter) error {
	c := s.repo.entClient(ctx)
	workQuery := func() *ent.SupplyImportItemQuery {
		q := c.SupplyImportItem.Query().Where(supplyimportitem.TaskID(task.ID))
		if len(payload.ActiveCodes) > 0 {
			q.Where(supplyimportitem.CodeIn(payload.ActiveCodes...))
		}
		return q
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		latest, err := s.repo.GetSyncTask(ctx, task.ID)
		if err != nil {
			return err
		}
		if !latest.CancelRequestedAt.IsZero() {
			return s.finishImport(ctx, task.ID, supplysynctask.StatusCanceled, "", "已停止，已导入商品保留；可继续未完成商品")
		}
		current, err := s.repo.GetConnection(ctx, conn.ID)
		if err != nil {
			return err
		}
		if payload.Fingerprint != importFingerprint(current) {
			return s.pauseImport(ctx, task.ID, "CONFIG_CHANGED", errImportConfiguration.Error())
		}

		item, err := workQuery().Where(supplyimportitem.StateIn("pending", "retry"), supplyimportitem.NextAttemptAtLTE(time.Now().Unix())).Order(supplyimportitem.ByID()).First(ctx)
		if ent.IsNotFound(err) {
			remaining, e := workQuery().Where(supplyimportitem.StateIn("pending", "retry")).Exist(ctx)
			if e != nil {
				return e
			}
			if remaining {
				if err := s.waitImport(ctx, task.ID, 2*time.Second); err != nil {
					return err
				}
				continue
			}
			failed, e := c.SupplyImportItem.Query().Where(supplyimportitem.TaskID(task.ID), supplyimportitem.StateEQ("failed")).Count(ctx)
			if e != nil {
				return e
			}
			code, summary := "", "导入完成"
			if failed > 0 {
				code = "PARTIAL_FAILED"
				summary = fmt.Sprintf("处理完成，%d 件商品需要处理；可查看问题并仅重试失败部分", failed)
			}
			// A stock-only retry must not resume unrelated canceled imports.
			unselected, e := c.SupplyImportItem.Query().Where(supplyimportitem.TaskID(task.ID), supplyimportitem.StateIn("pending", "retry")).Exist(ctx)
			if e != nil {
				return e
			}
			if unselected {
				return s.finishImport(ctx, task.ID, supplysynctask.StatusCanceled, code, "本次重试已完成，其余未处理商品保留；可继续未完成商品")
			}
			return s.finishImport(ctx, task.ID, supplysynctask.StatusDone, code, summary)
		}
		if err != nil {
			return err
		}
		if current.RateLimitUntil.After(time.Now()) {
			if err := s.waitImport(ctx, task.ID, min(2*time.Second, time.Until(current.RateLimitUntil))); err != nil {
				return err
			}
			continue
		}
		err = s.attemptImportItem(ctx, task, payload, conn, a, item)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if errors.Is(err, errTaskLeaseLost) {
				return err
			}
			if errors.Is(err, errImportConfiguration) {
				return s.pauseImport(ctx, task.ID, "CONFIG_CHANGED", err.Error())
			}
			failure := adapter.ClassifyImportError(err)
			if data.IsProductLocked(err) {
				failure = adapter.ImportFailure{Code: "PRODUCT_LOCKED", Summary: "商品已锁定，已跳过"}
			}
			if errors.Is(err, data.ErrLocalDeliveryProtected) {
				failure = adapter.ImportFailure{Code: "PRODUCT_CHANGED", Summary: "商品已配置本地发货，已跳过"}
			}
			if errors.Is(err, errImportChanged) {
				failure = adapter.ImportFailure{Code: "PRODUCT_CHANGED", Summary: errImportChanged.Error()}
			}
			if e := s.failImportItem(ctx, item, failure); e != nil {
				return e
			}
			s.log.Warn("supply.import.item_failed", "task_id", task.ID, "code", item.Code, "stage", item.Stage, "reason", failure.Code, "attempt", item.Attempts)
			if failure.Pause {
				return s.pauseImport(ctx, task.ID, failure.Code, failure.Summary)
			}
			if failure.Code == "RATE_LIMITED" {
				delay := importRetryDelay(item.Attempts, item.ID)
				if failure.After > delay {
					delay = failure.After
				}
				if s.pacer != nil {
					s.pacer.OnRateLimited(ctx, conn, failure.Summary)
				}
				fresh, e := s.repo.GetConnection(ctx, conn.ID)
				if e != nil {
					return e
				}
				until := time.Now().Add(delay)
				if fresh.RateLimitUntil.After(until) {
					until = fresh.RateLimitUntil
				}
				if _, e := c.SupplyConnection.UpdateOneID(conn.ID).SetRateLimitUntil(until).Save(ctx); e != nil {
					return e
				}
			}
		} else if s.pacer != nil {
			s.pacer.OnSuccess(ctx, conn)
		}
		delay := loadScheduleSettings(conn).PageDelay
		if s.pacer != nil {
			delay = s.pacer.Delay(conn)
		}
		if delay < 100*time.Millisecond {
			delay = 100 * time.Millisecond
		}
		if err := s.waitImport(ctx, task.ID, delay); err != nil {
			return err
		}
	}
}

func (s *SyncService) waitImport(ctx context.Context, taskID uint64, delay time.Duration) error {
	if err := s.repo.entClient(ctx).SupplySyncTask.UpdateOneID(taskID).SetHeartbeatAt(time.Now().UTC()).Exec(ctx); err != nil {
		return err
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func importRetryDelay(attempt int, id uint64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 3 {
		attempt = 3
	}
	return time.Duration(15*(1<<(attempt-1))+int(id%5)) * time.Second
}
func (s *SyncService) failImportItem(ctx context.Context, item *ent.SupplyImportItem, f adapter.ImportFailure) error {
	state, next := "failed", int64(0)
	if f.Code == "PRODUCT_LOCKED" || f.Code == "PRODUCT_CHANGED" {
		state = "skipped"
	} else if f.Retry && item.Attempts < importMaxAttempts {
		state = "retry"
		delay := importRetryDelay(item.Attempts, item.ID)
		if f.After > delay {
			delay = f.After
		}
		next = time.Now().Add(delay).Unix()
	}
	if state == "failed" && f.Retry {
		f.Summary = strings.TrimSuffix(f.Summary, "，正在等待重试") + "；自动重试已结束，可手动重试"
	}
	return data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		if err := s.guardTaskLease(ctx); err != nil {
			return err
		}
		return s.repo.entClient(ctx).SupplyImportItem.UpdateOneID(item.ID).SetState(state).SetNextAttemptAt(next).SetErrorCode(f.Code).SetErrorSummary(f.Summary).Exec(ctx)
	})
}
func (s *SyncService) pauseImport(ctx context.Context, id uint64, code, summary string) error {
	return s.finishImport(ctx, id, supplysynctask.StatusFailed, code, summary)
}
func (s *SyncService) finishImport(ctx context.Context, id uint64, status supplysynctask.Status, code, summary string) error {
	return data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		if err := s.guardTaskLease(ctx); err != nil {
			return err
		}
		return s.repo.FinishTask(ctx, id, status, code, summary)
	})
}

func (s *SyncService) validateImportItem(ctx context.Context, conn *ent.SupplyConnection, item *ent.SupplyImportItem) error {
	c := s.repo.entClient(ctx)
	if item.LocalProductID == 0 {
		exists, err := c.Product.Query().Where(product.UpstreamSourceID(conn.ID), product.UpstreamProductCode(item.Code), product.StatusGTE(0)).Exist(ctx)
		if err != nil {
			return err
		}
		if exists {
			return errImportChanged
		}
		return nil
	}
	p, err := data.GuardProductWrite(ctx, s.repo.data, item.LocalProductID)
	if ent.IsNotFound(err) {
		return errImportChanged
	}
	if err != nil {
		return err
	}
	if p.UpstreamSourceID != conn.ID || p.UpstreamProductCode != item.Code || productRevision(p) != item.LocalRevision {
		return errImportChanged
	}
	if e := data.GuardUpstreamDelivery(ctx, c, p); e != nil {
		return e
	}
	if item.Saved {
		return nil
	} // Stock recovery preserves prices and SKU shape.
	m, err := s.repo.GetMapping(ctx, conn.ID, item.Code, "")
	if err != nil {
		return errImportChanged
	}
	if last, ok := m.PricingOverride["last_synced_price"]; !ok || toInt64(last) != p.Price {
		return errImportChanged
	}
	rows, err := c.ProductSku.Query().Where(productsku.ProductID(p.ID)).All(ctx)
	if err != nil {
		return err
	}
	baselines, _ := m.PricingOverride["sku_prices"].(map[string]any)
	for _, sk := range rows {
		if sk.UpstreamSkuID != "" {
			if last, ok := baselines[sk.UpstreamSkuID]; !ok || toInt64(last) != sk.Price {
				return errImportChanged
			}
		}
	}
	return nil
}

func (s *SyncService) attemptImportItem(ctx context.Context, task *ent.SupplySyncTask, payload *importPayload, conn *ent.SupplyConnection, a adapter.Adapter, item *ent.SupplyImportItem) error {
	// Preflight avoids spending upstream calls on already-locked/edited products.
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		if err := s.guardTaskLease(ctx); err != nil {
			return err
		}
		if err := s.validateImportItem(ctx, conn, item); err != nil {
			return err
		}
		item.Attempts++
		return s.repo.entClient(ctx).SupplyImportItem.UpdateOneID(item.ID).SetAttempts(item.Attempts).Exec(ctx)
	})
	if err != nil {
		return err
	}
	if item.Stage == "stock" {
		return s.importStock(ctx, task, payload, conn, a, item)
	}
	var p adapter.Product
	if err := json.Unmarshal(item.Snapshot, &p); err != nil {
		return err
	}
	if q, ok := a.(adapter.AccountQuoter); ok && p.IsActive {
		quoteCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		quoted, err := q.QuoteProduct(quoteCtx, &p)
		cancel()
		if err != nil {
			return err
		}
		if quoted == nil {
			return fmt.Errorf("账号报价为空")
		}
		p = *quoted
	}
	// New products wait for confirmed stock before becoming sellable.
	p.Stock = -2
	p.StockCheckedAt = time.Now().UTC()
	mapping, err := s.repo.GetMapping(ctx, conn.ID, p.ID, "")
	if err != nil && err != ErrNotFound {
		return err
	}
	mediaCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	cover := s.coverFor(mediaCtx, mapping, conn, p.Cover)
	cancel()
	cp := &importCheckpoint{holdStock: item.LocalProductID == 0, cover: cover}
	if id, ok := payload.ProductCategories[item.Code]; ok {
		cp.categoryID = &id
	}
	cp.before = func(ctx context.Context) error {
		if cp.categoryID != nil && *cp.categoryID > 0 {
			ok, e := s.repo.entClient(ctx).Category.Query().Where(category.ID(*cp.categoryID), category.SubsiteID(payload.Tenant)).Exist(ctx)
			if e != nil {
				return e
			}
			if !ok {
				return fmt.Errorf("%w：目标分类已删除，请重新选择商品分类", errImportConfiguration)
			}
		}
		current, err := s.repo.GetConnection(ctx, conn.ID)
		if err != nil {
			return err
		}
		if importFingerprint(current) != payload.Fingerprint {
			return errImportConfiguration
		}
		return s.validateImportItem(ctx, conn, item)
	}
	cp.after = func(ctx context.Context, id uint64, created bool) error {
		local, err := s.repo.entClient(ctx).Product.Get(ctx, id)
		if err != nil {
			return err
		}
		return s.repo.entClient(ctx).SupplyImportItem.UpdateOneID(item.ID).SetSaved(true).SetSnapshot(json.RawMessage(`{}`)).SetCreated(created).SetLocalProductID(id).SetLocalRevision(productRevision(local)).SetStage("stock").SetState("pending").SetAttempts(0).SetNextAttemptAt(0).SetErrorCode("").SetErrorSummary("").SetActivateAfterStock(created && p.IsActive && payload.Mode != PriceModePending).Exec(ctx)
	}
	_, err = s.importOne(ctx, conn, &p, payload.Categories, payload.Mode, payload.Percent, payload.Amount, cp)
	return err
}

func (s *SyncService) importStock(ctx context.Context, task *ent.SupplySyncTask, payload *importPayload, conn *ent.SupplyConnection, a adapter.Adapter, item *ent.SupplyImportItem) error {
	started := time.Now().UTC()
	stockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	stock, err := a.GetStock(stockCtx, item.Code, "")
	cancel()
	if err != nil {
		return err
	}
	if stock < -1 {
		return fmt.Errorf("货源库存无效")
	}
	return data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		if err := s.guardTaskLease(ctx); err != nil {
			return err
		}
		current, err := s.repo.GetConnection(ctx, conn.ID)
		if err != nil {
			return err
		}
		if importFingerprint(current) != payload.Fingerprint {
			return errImportConfiguration
		}
		if err := s.validateImportItem(ctx, conn, item); err != nil {
			return err
		}
		if err := s.repo.recordStock(ctx, conn.ID, item.Code, "", stock, started); err != nil {
			return err
		}
		if item.ActivateAfterStock {
			// The revision check above prevents undoing a manual edit/unlisting.
			if err := s.repo.entClient(ctx).Product.UpdateOneID(item.LocalProductID).SetStatus(1).Exec(ctx); err != nil {
				return err
			}
		}
		return s.repo.entClient(ctx).SupplyImportItem.UpdateOneID(item.ID).SetState("done").SetNextAttemptAt(0).SetErrorCode("").SetErrorSummary("").Exec(ctx)
	})
}
