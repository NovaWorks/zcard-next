package supply

// 货源同步服务（// + S1 同步引擎改造）：
// - 三类 scope：collect（采集：upsert + 库存 + 删除对账）/ price（仅刷价格）/
// status（仅刷上下架 + up_stock）——轻量 scope 走 maintainer 端口不建不删
// - 状态增量：支持 IncrementalLister 时按锚点拉取变更；价格/采集全量核价，
// 避免账号优惠变化却未更新商品时间时漏同步。
// - 删除对账：仅权威快照（全量 + IncludesInactive 回声）做——seenCodes 对账
// 把上游已消失商品批量下架；护栏：上游声称 total > 实际处理数 → 任务失败
// 不删（宁可保守，1.x「不能批量误删」纪律）
// - 请求节流：分页页间 request_delay（settings.schedule，防上游限流封 IP）；
// 库存补查分批并发 + 批次间隔 + 600s 限速预算（1.x AcgFakaDriver 同款参数）
// - 任务追踪：进度/心跳(30s)/统计/取消标志；失败 error_context 落库
// - 库存失败保持未知，汇总失败商品；商品导入完成不等于库存查询成功
// - 终态发布 sync.completed 事件（ 告警 / 对账数据源）

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplysynctask"
)

// SyncTaskType 同步任务队列类型（asynq mux 精确匹配；low 队列）。
const SyncTaskType = "supply.sync"

// 同步 scope（任务粒度；空 = collect 兼容历史任务）。
const (
	ScopeCollect = "collect"
	ScopePrice   = "price"
	ScopeStatus  = "status"
	ScopeStock   = "stock" // 仅补查已映射商品库存，不写价格、分类、上下架
)

// 节流/补查默认参数（settings.schedule 可覆盖；对齐 1.x AcgFakaDriver）。
const (
	defaultPageDelaySec   = 1       // 商品分页页间间隔（秒；0=不限）
	defaultStockConc      = 3       // 库存补查并发（1-10）
	defaultStockBatchMs   = 200     // 补查批次间隔（毫秒）
	stockThrottleBudgetMs = 600_000 // 补查限速预算（600s，超出报错提示调参）
)

// scheduleSettings 连接级节流参数（ ；S2 自适应节奏器在此基础上倍增）。
type scheduleSettings struct {
	PageDelay       time.Duration // 商品分页页间隔
	StockConc       int           // 库存补查并发
	StockBatchDelay time.Duration // 补查批次间隔
}

func loadScheduleSettings(conn *ent.SupplyConnection) scheduleSettings {
	var sched map[string]any
	if conn != nil {
		sched, _ = conn.Settings["schedule"].(map[string]any)
	}
	if sched == nil {
		sched = map[string]any{}
	}
	delaySec := toInt(sched["request_delay"], defaultPageDelaySec)
	conc := toInt(sched["stock_concurrency"], defaultStockConc)
	if conc < 1 {
		conc = 1
	}
	if conc > 10 {
		conc = 10
	}
	batchMs := toInt(sched["stock_request_delay_ms"], defaultStockBatchMs)
	return scheduleSettings{
		PageDelay:       time.Duration(delaySec) * time.Second,
		StockConc:       conc,
		StockBatchDelay: time.Duration(batchMs) * time.Millisecond,
	}
}

func toInt(v any, def int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return def
}

// SyncService 同步执行器。
type SyncService struct {
	repo       *SupplyRepoImpl
	writer     catalogport.UpstreamProductWriter
	maintainer catalogport.UpstreamProductMaintainer // 轻量 scope + 删除对账（nil=跳过）
	pacer      *Pacer                                // 自适应节奏器（nil=静态节流，测试用）
	enq        queue.Enqueuer
	outbox     events.Writer // sync.completed 发布
	log        *slog.Logger

	// 封面采集去重缓存（url → 本地 /uploads/ 路径或完整上游 URL；mutex 保护并发任务）
	coverMu    sync.Mutex
	coverCache map[string]string
	coverDirs  map[uint64]string // connectionID → 渠道封面目录名（ensureCoverDir 缓存）
}

// NewSyncService 构造。
func NewSyncService(repo *SupplyRepoImpl, writer catalogport.UpstreamProductWriter, maintainer catalogport.UpstreamProductMaintainer, pacer *Pacer, enq queue.Enqueuer, outbox events.Writer, log *slog.Logger) *SyncService {
	return &SyncService{repo: repo, writer: writer, maintainer: maintainer, pacer: pacer, enq: enq, outbox: outbox, log: log}
}

// StartTask 调度同步任务：有 Redis 入 low 队列；无 Redis（或未装配，测试）直接
// 异步执行（降级串行语义）。
func (s *SyncService) StartTask(ctx context.Context, taskID uint64) error {
	payload, err := json.Marshal(map[string]uint64{"task_id": taskID})
	if err != nil {
		return err
	}
	if s.enq != nil && s.enq.Enabled() {
		return s.enq.Enqueue(ctx, queue.Task{
			Type:      SyncTaskType,
			Payload:   payload,
			Queue:     queue.QueueLow,
			DedupeKey: SyncTaskType + ":" + strconv.FormatUint(taskID, 10),
		})
	}
	// 降级：进程内异步（失败由任务终态 failed 落库，与 asynq 语义一致）
	go func() {
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
		defer cancel()
		if err := s.RunTask(runCtx, payload); err != nil {
			s.log.Error("supply.sync.run_failed", "task_id", taskID, "err", err)
		}
	}()
	return nil
}

// RunTask 队列 worker 入口（payload = {"task_id": N}）。
func (s *SyncService) RunTask(ctx context.Context, payload []byte) error {
	var req struct {
		TaskID uint64 `json:"task_id"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("supply.sync: 解析任务载荷失败: %w", err)
	}
	return s.RunSync(ctx, req.TaskID)
}

// RunSync 执行一次同步（worker 与降级路径共用；重复入队由任务状态幂等）。
func (s *SyncService) RunSync(ctx context.Context, taskID uint64) error {
	task, err := s.repo.GetSyncTask(ctx, taskID)
	if err != nil {
		return err
	}
	// 幂等：终态任务直接 ACK（重复投递不重跑）
	if task.Status != supplysynctask.StatusPending {
		return nil
	}
	scope := task.Scope
	if scope == "" {
		scope = ScopeCollect // 历史任务兼容
	}
	if scope != ScopeCollect && scope != ScopePrice && scope != ScopeStatus && scope != ScopeStock {
		_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "INVALID_SCOPE", "scope 必须为 collect|price|status|stock")
		return nil
	}
	conn, err := s.repo.GetConnection(ctx, task.ConnectionID)
	if err != nil {
		return err
	}
	// 熔断冷却中：任务直接失败留痕（定时调度会跳过冷却中的渠道——这里兜底
	// 手动触发/冷却期内已入队的任务；不重试）
	if s.pacer != nil && s.pacer.CooldownActive(conn) {
		_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "RATE_LIMITED_COOLDOWN",
			"渠道熔断冷却中（上游限流），稍后重试")
		s.publishCompleted(ctx, conn.ID, taskID, "failed")
		return nil
	}
	credsJSON, err := s.repo.OpenCredentials(conn)
	if err != nil {
		_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "CREDENTIALS_DECRYPT_FAILED", err.Error())
		return nil // 凭据损坏不重试（提示重配），避免死循环
	}
	var creds adapter.Credentials
	if err := json.Unmarshal([]byte(credsJSON), &creds); err != nil {
		_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "CREDENTIALS_INVALID", err.Error())
		return nil
	}
	a, err := adapter.New(conn.Driver, conn.BaseURL, creds, parseRetryIntervals(conn.RetryIntervals))
	if err != nil {
		_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "ADAPTER_NEW", err.Error())
		return nil
	}

	// 锁：按连接分组（无 Redis 时串行；任务状态机本身防并发重入）
	if err := s.repo.SetTaskProcessing(ctx, taskID, 0); err != nil {
		return err
	}

	sched := loadScheduleSettings(conn)
	if scope == ScopeStock {
		err := s.runStockOnly(ctx, task, conn, a, sched)
		if err != nil {
			_ = s.repo.FinishTask(ctx, task.ID, supplysynctask.StatusFailed, "STOCK_QUERY_FAILED", adapter.StockErrorSummary(err))
			s.publishCompleted(ctx, conn.ID, task.ID, "failed")
		}
		return err
	}

	// 列表函数解析：增量（驱动支持 + 有锚点）→ 全量回落。
	// 增量快照不具对账权威性（未见 ≠ 已删除）→ authoritative 仅在全量且回声完整时成立。
	list, incremental := resolveLister(a, task, readSyncAnchor(conn, scope), s.log)
	return s.runLoop(ctx, taskID, task, conn, a, sched, scope, incremental, list)
}

// resolveLister 列表函数决策：
// 驱动实现 IncrementalLister 且锚点有效 → 增量（含下架变更）；否则全量。
func resolveLister(a adapter.Adapter, task *ent.SupplySyncTask, anchor time.Time, log *slog.Logger) (func(ctx context.Context, page, pageSize int) (*adapter.ProductList, error), bool) {
	// Account discounts can change without a product updated_at change. Any scope
	// that writes prices must inspect the full catalog, even when incremental was requested.
	if task.Mode != "incremental" || anchor.IsZero() || task.Scope == ScopePrice || task.Scope == ScopeCollect || task.Scope == "" {
		return func(ctx context.Context, page, pageSize int) (*adapter.ProductList, error) {
			return a.ListProducts(ctx, page, pageSize, true)
		}, false
	}
	if il, ok := a.(adapter.IncrementalLister); ok {
		after := anchor.Add(-1 * time.Minute) // 安全窗（时钟偏差，dujiao-next 同款）
		log.Info("supply.sync.incremental", "after", after.Format(time.RFC3339))
		return func(ctx context.Context, page, pageSize int) (*adapter.ProductList, error) {
			return il.ListProductsAfter(ctx, page, pageSize, after)
		}, true
	}
	log.Info("supply.sync.incremental_unsupported_fallback_full", "driver", a.Protocol())
	return func(ctx context.Context, page, pageSize int) (*adapter.ProductList, error) {
		return a.ListProducts(ctx, page, pageSize, true)
	}, false
}

// runLoop 分页主循环（增量/全量共用；incremental 决定是否允许删除对账）。
func (s *SyncService) runLoop(ctx context.Context, taskID uint64, task *ent.SupplySyncTask, conn *ent.SupplyConnection, a adapter.Adapter, sched scheduleSettings, scope string, incremental bool, list func(ctx context.Context, page, pageSize int) (*adapter.ProductList, error)) error {
	stats := TaskProgress{Stage: "fetching_products", Page: 1}
	heartbeat := time.Now()

	// 分类映射缓存：upstream_category → local_category_id（仅 collect 写分类）
	categoryMap := map[string]uint64{}
	if scope == ScopeCollect {
		if err := s.cacheCategoryMappings(ctx, a, conn, categoryMap); err != nil {
			s.log.Warn("supply.sync.categories_failed", "connection_id", conn.ID, "err", err)
		}
	}

	// 对账状态：seenCodes（权威快照时收集）；reportedTotal 护栏基准
	authoritative := !incremental
	seen := map[string]bool{}
	reportedTotal := 0
	processed := 0
	stockTotal, stockFailed := 0, 0
	var stockErrors []string

	page := 1
	for {
		if !heartbeat.IsZero() && time.Since(heartbeat) >= 30*time.Second {
			cancel, err := s.repo.TouchTask(ctx, taskID, stats)
			if err != nil {
				s.failAndMaybeRetry(ctx, taskID, conn.ID, "HEARTBEAT_FAILED", err.Error())
				return nil
			}
			if cancel {
				_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusCanceled, "", "")
				s.publishCompleted(ctx, conn.ID, taskID, "canceled")
				return nil
			}
			stats = TaskProgress{}
			heartbeat = time.Now()
		}

		listedAt := time.Now().UTC()
		list0, err := list(ctx, page, 50)
		if err != nil {
			// fail-open：拉取失败 → 已处理部分保留，任务失败留痕（可重跑）。
			// 限流信号反馈节奏器（AIMD 降速/熔断判据）
			if s.pacer != nil && errors.Is(err, adapter.ErrRateLimited) {
				s.pacer.OnRateLimited(ctx, conn, err.Error())
			}
			s.failAndMaybeRetry(ctx, taskID, conn.ID, "LIST_PRODUCTS_FAILED", err.Error())
			s.publishCompleted(ctx, conn.ID, taskID, "failed")
			return nil
		}
		// 页成功：反馈节奏器（连续成功间隔回升）+ 动态页间隔（自适应值 > 配置底线）
		if s.pacer != nil {
			s.pacer.OnSuccess(ctx, conn)
		}
		pageDelay := sched.PageDelay
		if s.pacer != nil {
			pageDelay = s.pacer.Delay(conn)
		}
		if authoritative && !list0.IncludesInactive {
			// 上游未回声 include_inactive → 快照不完整，禁用删除对账（防误删）
			authoritative = false
			s.log.Info("supply.sync.reconcile_disabled_no_echo", "task_id", taskID)
		}
		if list0.Total > reportedTotal {
			reportedTotal = list0.Total
		}
		stats.Page = page

		for i := range list0.Items {
			list0.Items[i].StockCheckedAt = listedAt
		}
		// 采集和状态同步统一补查；失败项保持未知并汇总。
		if scope == ScopeCollect || scope == ScopeStatus {
			if conn.Driver == "acg_faka" {
				for i := range list0.Items {
					list0.Items[i].Stock = -2
				}
			}
			if err := s.backfillStocks(ctx, a, sched, list0.Items, taskID); err != nil {
				if errors.Is(err, errStockCanceled) {
					_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusCanceled, "", "")
					s.publishCompleted(ctx, conn.ID, taskID, "canceled")
					return nil
				}
				_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "STOCK_BACKFILL_BUDGET", err.Error())
				s.publishCompleted(ctx, conn.ID, taskID, "failed")
				return nil
			}
		}

		if scope == ScopeCollect || scope == ScopeStatus {
			for _, p := range list0.Items {
				stockTotal++
				if p.Stock < -1 {
					stockFailed++
					if len(stockErrors) < 10 {
						stockErrors = append(stockErrors, fmt.Sprintf("%s: %s", p.ID, p.StockError))
					}
				}
			}
		}
		for i := range list0.Items {
			if time.Since(heartbeat) >= 30*time.Second {
				canceled, err := s.repo.TouchTask(ctx, taskID, stats)
				if err != nil {
					return err
				}
				stats = TaskProgress{Stage: "pricing", Page: page}
				heartbeat = time.Now()
				if canceled {
					_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusCanceled, "", "")
					s.publishCompleted(ctx, conn.ID, taskID, "canceled")
					return nil
				}
			}
			if quoter, ok := a.(adapter.AccountQuoter); ok && list0.Items[i].IsActive && (scope == ScopeCollect || scope == ScopePrice) {
				shouldQuote := true
				if scope == ScopePrice {
					_, err := s.repo.GetMapping(ctx, conn.ID, list0.Items[i].ID, "")
					if err == ErrNotFound {
						shouldQuote = false
					} else if err != nil {
						return err
					}
				}
				if shouldQuote {
					quoted, err := quoter.QuoteProduct(ctx, &list0.Items[i])
					if err != nil {
						if s.pacer != nil && errors.Is(err, adapter.ErrRateLimited) {
							s.pacer.OnRateLimited(ctx, conn, err.Error())
						}
						s.failAndMaybeRetry(ctx, taskID, conn.ID, "PRICE_QUOTE_FAILED", "商品 "+list0.Items[i].ID+" 账号报价失败，已保留原价格，请重试")
						s.publishCompleted(ctx, conn.ID, taskID, "failed")
						return nil
					}
					list0.Items[i] = *quoted
				}
			}
			cancel, err := s.syncOne(ctx, taskID, task, conn, &list0.Items[i], categoryMap, &stats)
			if err != nil {
				s.failAndMaybeRetry(ctx, taskID, conn.ID, "SYNC_ITEM_FAILED", err.Error())
				s.publishCompleted(ctx, conn.ID, taskID, "failed")
				return nil
			}
			if cancel {
				_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusCanceled, "", "")
				s.publishCompleted(ctx, conn.ID, taskID, "canceled")
				return nil
			}
			processed++
			if authoritative {
				seen[list0.Items[i].ID] = true
			}
		}
		if !list0.HasMore {
			break
		}
		page++
		// 页间节流（自适应节奏器优先；防上游限流封 IP）
		if err := sleepCtx(ctx, pageDelay); err != nil {
			_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "CTX_CANCELED", err.Error())
			return nil
		}
	}

	// 删除对账（仅 collect + 权威快照）：
	// 护栏——上游声称总数 > 实际处理数说明分页不完整，宁可不删也不能批量误删。
	if scope == ScopeCollect && authoritative {
		if reportedTotal > processed {
			_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "RECONCILE_GUARD",
				fmt.Sprintf("上游声称 %d 件但仅处理 %d 件（分页不完整），跳过删除对账防误删", reportedTotal, processed))
			s.publishCompleted(ctx, conn.ID, taskID, "failed")
			return nil
		}
		if s.maintainer != nil && len(seen) > 0 {
			codes := make([]string, 0, len(seen))
			for c := range seen {
				codes = append(codes, c)
			}
			shelved, err := s.maintainer.ShelveOffMissing(ctx, conn.ID, codes)
			if err != nil {
				_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "RECONCILE_SHELVE_FAILED", err.Error())
				s.publishCompleted(ctx, conn.ID, taskID, "failed")
				return nil
			}
			stats.Deleted += int32(shelved)
		}
	}

	// 收尾：锚点（增量依据）+ last_synced_at（collect 沿用旧列）+ done
	if _, err := s.repo.TouchTask(ctx, taskID, stats); err == nil {
		s.writeSyncAnchor(ctx, conn, scope)
		if scope == ScopeCollect {
			now := time.Now().UTC()
			client := s.repo.entClient(ctx)
			_, _ = client.SupplyConnection.UpdateOneID(conn.ID).SetLastSyncedAt(now).Save(ctx)
		}
	}
	if stockFailed > 0 {
		_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "STOCK_QUERY_FAILED", fmt.Sprintf("商品同步已完成；库存成功 %d，失败 %d。可仅重试失败库存。%s", stockTotal-stockFailed, stockFailed, strings.Join(stockErrors, "；")))
		s.publishCompleted(ctx, conn.ID, taskID, "failed")
		return nil
	}
	_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusDone, "", "")
	clearSyncRetry(taskID)
	s.publishCompleted(ctx, conn.ID, taskID, "done")
	return nil
}

// syncOne 同步单个商品（scope 分支）：
// - collect：价格保护 → upsert 商品 → upsert 映射（up_stock 缓存）
// - price：仅已映射商品刷价格（价格保护同口径；不建不删不动库存）
// - status：仅已映射商品刷上下架 + up_stock（不建不删不动价格）
//
// 返回 (cancelRequested, error)。
func (s *SyncService) syncOne(ctx context.Context, taskID uint64, task *ent.SupplySyncTask, conn *ent.SupplyConnection, p *adapter.Product, categoryMap map[string]uint64, stats *TaskProgress) (bool, error) {
	// Fetch media before opening the pricing transaction.
	cover := ""
	if task.Scope == "" || task.Scope == ScopeCollect {
		m, err := s.repo.GetMapping(ctx, conn.ID, p.ID, "")
		if err != nil && err != ErrNotFound {
			return false, err
		}
		if p.IsActive {
			cover = s.coverFor(ctx, m, conn, p.Cover)
		}
	}
	next := *stats
	canceled := false
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		if err := s.checkPricingConnection(ctx, conn); err != nil {
			return err
		}
		var err error
		canceled, err = s.syncOneLocked(ctx, taskID, task, conn, p, categoryMap, &next, cover)
		return err
	})
	if data.IsProductLocked(err) {
		stats.ManualSkipped++
		stats.Processed++
		return false, nil
	}
	if err == nil {
		*stats = next
	}
	return canceled, err
}

func (s *SyncService) syncOneLocked(ctx context.Context, taskID uint64, task *ent.SupplySyncTask, conn *ent.SupplyConnection, p *adapter.Product, categoryMap map[string]uint64, stats *TaskProgress, preparedCover string) (bool, error) {
	mapping, err := s.repo.GetMapping(ctx, conn.ID, p.ID, "")
	notFound := err == ErrNotFound
	if err != nil && !notFound {
		return false, err
	}

	if !notFound && mapping.LocalProductID > 0 {
		if _, e := data.GuardProductWrite(ctx, s.repo.data, mapping.LocalProductID); e != nil {
			return false, e
		}
	}
	// ── 轻量 scope：无映射（未导入）直接跳过，绝不创建 ──
	if task.Scope == ScopePrice || task.Scope == ScopeStatus {
		if notFound {
			return false, nil
		}
		if task.Scope == ScopePrice {
			return false, s.syncPriceOnly(ctx, conn, mapping, p, task.ForceReprice, stats)
		}
		return false, s.syncStatusOnly(ctx, conn, mapping, p, stats)
	}

	if notFound {
		mapping = &ent.SupplyMapping{ConnectionID: conn.ID, UpstreamProduct: p.ID}
	}

	// 状态语义：上游不可售（手动发货/预选/停售）→ 本地下架(0)。
	// （曾用隐藏(2)——但隐藏商品会员可见可买，此类品 API 无法履约，必须全员下架）
	// 下架清空采集封面引用；不在事务提交前删除文件，避免影响其他商品的共享封面。
	// 上游可售(1)不覆盖本地已下架品——手动下架的运营意图优先（同步只下架不上架）
	status := int8(1)
	if !p.IsActive {
		status = 0
		p.Cover = ""
	} else if !notFound && s.localProductShelvedOff(ctx, mapping.LocalProductID) {
		status = 0 // 保持本地手动下架（不写 1 拉回）
	}

	if err := s.lockProductPricing(ctx, mapping); err != nil {
		return false, err
	}
	priceToWrite, skus, override, err := s.productPrices(ctx, conn, mapping, p, task.ForceReprice)
	if err != nil {
		return false, err
	}
	writePrice := priceToWrite >= 0
	if !writePrice && mapping.LocalProductID == 0 {
		status = 0
	}

	// upsert 本地商品（价格 -1 = 不更新）
	write := catalogport.UpstreamProductInput{
		ConnectionID:        conn.ID,
		UpstreamProductCode: p.ID,
		UpstreamSyncedAt:    time.Now().UTC(),
		Name:                p.Name,
		Description:         p.Description,
		DescriptionSet:      p.DescriptionSet,
		Cover:               preparedCover,
		FactoryPrice:        accountCost(conn, p),
		Status:              status,
		AutoOnshelf:         autoOnshelf(conn.Settings),
	}
	if localCat, ok := categoryMap[p.CategoryID]; ok {
		write.CategoryID = localCat
		write.CategorySet = true
	}
	if !writePrice {
		write.Price = -1
		stats.ManualSkipped++
	} else {
		write.Price = priceToWrite
		stats.PriceUpdated++
	}
	write.SKUs = skus

	productID, created, err := s.writeProductCategory(ctx, p.CategoryID, &write)
	if err != nil {
		return false, err
	}
	if created {
		stats.Created++
	} else {
		stats.Updated++
	}
	stats.Processed++

	// upsert 映射（up_stock 缓存 + pricing_override 持久化）
	mapping.LocalProductID = productID
	mapping.UpstreamCategory = p.CategoryID
	if write.CategorySet {
		mapping.LocalCategoryID = write.CategoryID
	}
	mapping.UpStock = p.Stock
	mapping.StockCheckedAt = stockObservationTime(p)
	mapping.PricingOverride = override
	if err := s.saveProductMapping(ctx, mapping); err != nil {
		return false, err
	}

	// 取消检查（每商品粒度太细，放每页尾部；此处仅任务级取消标志）
	return false, nil
}

// syncPriceOnly price scope 轻路径：价格保护 → 仅更新价格 + 基线持久化。
func (s *SyncService) syncPriceOnly(ctx context.Context, conn *ent.SupplyConnection, mapping *ent.SupplyMapping, p *adapter.Product, force bool, stats *TaskProgress) error {
	next := *stats
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := s.repo.entClient(ctx)
		if err := s.checkPricingConnection(ctx, conn); err != nil {
			return err
		}
		latest, err := s.repo.GetMapping(ctx, conn.ID, p.ID, "")
		if err != nil {
			return err
		}
		if err := s.lockProductPricing(ctx, latest); err != nil {
			return err
		}
		price, skus, override, err := s.productPrices(ctx, conn, latest, p, force)
		if err != nil {
			return err
		}
		if s.maintainer == nil {
			return nil
		}
		hasPrice := price >= 0
		for _, sk := range skus {
			hasPrice = hasPrice || sk.PriceCents >= 0
		}
		if hasPrice {
			found, err := s.maintainer.UpdateUpstreamPrice(ctx, conn.ID, p.ID, price, skus...)
			if err != nil {
				return err
			}
			if !found {
				return nil
			}
		}

		if price >= 0 {
			next.PriceUpdated++
		} else {
			next.ManualSkipped++
		}
		// Cost is an account quote, independent of local sale-price protection.
		if cost := accountCost(conn, p); cost > 0 {
			if err := c.Product.Update().Where(product.ID(latest.LocalProductID), product.StatusGTE(0)).SetFactoryPrice(cost).Exec(ctx); err != nil {
				return err
			}
		}
		latest.PricingOverride = override
		if err := s.repo.UpsertMapping(ctx, latest); err != nil {
			return err
		}
		next.Processed++
		return nil
	})
	if err == nil {
		*stats = next
	}
	return err
}

// localProductShelvedOff 本地商品当前是否下架(0)——手动下架保持判据。
func (s *SyncService) localProductShelvedOff(ctx context.Context, localProductID uint64) bool {
	if localProductID == 0 {
		return false
	}
	st, err := s.repo.entClient(ctx).Product.Query().
		Where(product.ID(localProductID)).
		Select(product.FieldStatus).
		Int(ctx)
	return err == nil && st == 0
}

// syncStatusOnly uses the normalized stock after upstream backfill (-1 unlimited, -2 unknown).
// 单向语义：只传导「下架」，不自动上架——运营在后台手动下架的商品不被同步
// 拉回（重新上架走后台操作；上游可售≠本地必须卖）。
func (s *SyncService) syncStatusOnly(ctx context.Context, conn *ent.SupplyConnection, mapping *ent.SupplyMapping, p *adapter.Product, stats *TaskProgress) error {
	if s.maintainer != nil && !p.IsActive {
		if _, err := s.maintainer.UpdateUpstreamStatus(ctx, conn.ID, p.ID, 0); err != nil {
			return err
		}
	}
	if p.Stock >= -2 {
		mapping.UpStock = p.Stock
		mapping.StockCheckedAt = stockObservationTime(p)
	}
	if err := s.repo.UpsertMapping(ctx, mapping); err != nil {
		return err
	}
	stats.Updated++
	stats.Processed++
	return nil
}

// backfillStocks bounds each read independently; failures do not erase references.
var errStockCanceled = errors.New("库存补查已取消")

func stockObservationTime(p *adapter.Product) time.Time {
	if p.StockCheckedAt.IsZero() {
		return time.Now().UTC()
	}
	return p.StockCheckedAt
}

func (s *SyncService) backfillStocks(ctx context.Context, a adapter.Adapter, cfg scheduleSettings, items []adapter.Product, taskID uint64) error {
	var missing []int
	for i := range items {
		// ACG 的目录库存由调用方先标为未知；其他协议明确的 0/-1 保持原语义。
		if items[i].Stock < -1 {
			missing = append(missing, i)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	chunks := (len(missing) + cfg.StockConc - 1) / cfg.StockConc
	if throttleMs := int64(chunks-1) * cfg.StockBatchDelay.Milliseconds(); throttleMs > stockThrottleBudgetMs {
		return fmt.Errorf("库存补查限速配置预计等待 %d 秒（items=%d concurrency=%d batch_delay_ms=%d），请提高并发数或缩短批次间隔",
			(throttleMs+999)/1000, len(missing), cfg.StockConc, cfg.StockBatchDelay.Milliseconds())
	}
	var rateLimited atomic.Bool
	for ci := 0; ci < chunks; ci++ {
		if taskID > 0 {
			canceled, err := s.repo.TouchTask(ctx, taskID, TaskProgress{Stage: "fetching_stock"})
			if err != nil {
				return err
			}
			if canceled {
				return errStockCanceled
			}
		}
		lo := ci * cfg.StockConc
		hi := lo + cfg.StockConc
		if hi > len(missing) {
			hi = len(missing)
		}
		var wg sync.WaitGroup
		for _, i := range missing[lo:hi] {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				items[i].StockCheckedAt = time.Now().UTC()
				bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
				defer cancel()
				st, err := a.GetStock(bounded, items[i].ID, "")
				if err != nil {
					if errors.Is(err, adapter.ErrRateLimited) {
						rateLimited.Store(true)
					}
					items[i].StockError = adapter.StockErrorSummary(err)
					s.log.Warn("supply.sync.stock_backfill_failed", "task_id", taskID, "code", items[i].ID, "reason", items[i].StockError)
					items[i].Stock = -2
					return // 未知库存不可冒充无限或售罄
				}
				items[i].Stock = st
				if st < -1 {
					items[i].Stock = -2
					items[i].StockError = "上游未返回有效库存"
				}
			}(i)
		}
		wg.Wait()
		if rateLimited.Load() {
			// Do not continue probing the rest of a catalog after a WAF/429.
			for _, i := range missing[hi:] {
				items[i].Stock = -2
				items[i].StockError = "货源限流或网关拦截，待稍后补查"
				items[i].StockCheckedAt = time.Now().UTC()
			}
			return nil
		}
		if ci < chunks-1 {
			if err := sleepCtx(ctx, cfg.StockBatchDelay); err != nil {
				return err
			}
		}
	}
	return nil
}

// cacheCategoryMappings 构建 上游分类标识 → 本地分类 id 映射。
// 优先级：连接 settings.category_map（导入弹窗持久化的运营选择，后续同步
// 自动沿用）→ mapping 行（upstream_category 手工映射）；无映射分类不自动建。
func (s *SyncService) cacheCategoryMappings(ctx context.Context, a adapter.Adapter, conn *ent.SupplyConnection, out map[string]uint64) error {
	cats, err := a.ListCategories(ctx)
	if err != nil {
		return err
	}
	_ = cats
	for k, v := range categoryMapFromSettings(conn.Settings) {
		out[k] = v
	}
	// 本地分类映射表：扫描本连接全部 mapping，收集 upstream_category → local_category_id
	ms, _, err := s.repo.ListMappings(ctx, conn.ID, 1, 100000)
	if err != nil {
		return err
	}
	for _, m := range ms {
		if m.LocalCategoryID > 0 && m.UpstreamCategory != "" {
			if _, ok := out[m.UpstreamCategory]; !ok {
				out[m.UpstreamCategory] = m.LocalCategoryID
			}
		}
	}
	return nil
}

// categoryMapFromSettings 连接 settings.category_map（JSON {上游分类code: 本地分类id}）。
func categoryMapFromSettings(settings map[string]any) map[string]uint64 {
	out := map[string]uint64{}
	if settings == nil {
		return out
	}
	raw, ok := settings["category_map"].(map[string]any)
	if !ok {
		return out
	}
	for k, v := range raw {
		if id := toInt64(v); id >= 0 && (fmt.Sprint(v) == "0" || id > 0) {
			out[k] = uint64(id)
		}
	}
	return out
}

// currentProductPrice 读本地商品当前价（价格保护判据）。
func (s *SyncService) currentProductPrice(ctx context.Context, productID uint64) (int64, error) {
	if productID == 0 {
		return 0, fmt.Errorf("supply: 无本地商品")
	}
	client := s.repo.entClient(ctx)
	p, err := client.Product.Get(ctx, productID)
	if err != nil {
		return 0, err
	}
	return p.Price, nil
}

// ensureCoverDir 渠道封面目录名（连接级缓存 + settings.cover_dir 持久化）：
// - settings.cover_dir 已有 → 沿用（重启稳定）
// - 否则扫描 uploads/ 一级目录：渠道名净化后取首个空闲名（重名加 2/3……），
// 并写入 settings.cover_dir（落库失败仅告警，下次重新解析）
func (s *SyncService) ensureCoverDir(ctx context.Context, conn *ent.SupplyConnection) string {
	s.coverMu.Lock()
	if dir, ok := s.coverDirs[conn.ID]; ok {
		s.coverMu.Unlock()
		return dir
	}
	s.coverMu.Unlock()

	dir := ""
	if conn.Settings != nil {
		if v, ok := conn.Settings["cover_dir"].(string); ok && v != "" {
			dir = v
		}
	}
	if dir == "" {
		dir = allocateCoverDir(listUploadSubDirs(), sanitizeSubDir(conn.Name))
		if dir != "" {
			if saved, err := s.saveCoverDirectory(ctx, conn.ID, dir); err != nil {
				s.log.Warn("supply.cover_dir_save_failed", "connection_id", conn.ID, "err", err)
			} else {
				dir = saved
			}
		}
	}
	s.coverMu.Lock()
	if s.coverDirs == nil {
		s.coverDirs = map[uint64]string{}
	}
	s.coverDirs[conn.ID] = dir
	s.coverMu.Unlock()
	return dir
}

// coverFor 解析封面并落本地：下载（fail-open）→ 与旧 cover 比对，本地旧文件
// 换图/清空时删除（防泄漏）。mapping 可为 nil（新建）。
func (s *SyncService) coverFor(ctx context.Context, mapping *ent.SupplyMapping, conn *ent.SupplyConnection, cover string) string {
	if mapping != nil {
		row, err := data.Client(ctx, s.repo.data).Product.Get(ctx, mapping.LocalProductID)
		if err == nil {
			if row.IsLocked || row.CoverProtected {
				return row.Cover
			}
		}
	}
	dir := s.ensureCoverDir(ctx, conn)
	newCover := s.downloadCover(ctx, conn.BaseURL, cover, dir)
	// Reference-aware media cleanup owns deletion after the write commits.
	return newCover
}

// readSyncAnchor 读取 scope 增量锚点（优先专用列；回退 settings.sync_anchors
// ——S1 过渡期写入的兼容；collect 再回退旧列 last_synced_at）。
func readSyncAnchor(conn *ent.SupplyConnection, scope string) time.Time {
	if t := scopeAnchor(conn, scope); !t.IsZero() {
		return t
	}
	anchors, _ := conn.Settings["sync_anchors"].(map[string]any)
	if v, ok := anchors[scope].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// writeSyncAnchor 写 scope 增量锚点（专用列；collect 同时沿用 last_synced_at 旧列）。
func (s *SyncService) writeSyncAnchor(ctx context.Context, conn *ent.SupplyConnection, scope string) {
	_ = s.repo.TouchScopeAnchor(ctx, conn.ID, scope)
	if scope == ScopeCollect {
		_, _ = s.repo.entClient(ctx).SupplyConnection.UpdateOneID(conn.ID).
			SetLastSyncedAt(time.Now().UTC()).Save(ctx)
	}
}

// sleepCtx 可取消 sleep（节流等待期间响应任务取消/超时）。
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// publishCompleted 发布 sync.completed 事件（终态：done/failed/canceled）。
func (s *SyncService) publishCompleted(ctx context.Context, connectionID, taskID uint64, status string) {
	if s.outbox == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"task_id":       taskID,
		"connection_id": connectionID,
		"status":        status,
	})
	if err != nil {
		s.log.Warn("supply.sync.publish_payload_failed", "err", err)
		return
	}
	aggID := "sync:" + strconv.FormatUint(taskID, 10)
	if err := s.outbox.Write(ctx, "supply", events.SyncCompleted, aggID, aggID, payload); err != nil {
		s.log.Warn("supply.sync.publish_failed", "task_id", taskID, "err", err)
	}
}

// autoOnshelf 新建商品自动上架开关（settings.auto_onshelf，默认 true）。
func autoOnshelf(settings map[string]any) bool {
	v, ok := settings["auto_onshelf"]
	if !ok {
		return true
	}
	b, _ := v.(bool)
	return b
}

// ImportOne 单品导入（ D 交互式导入；与 collect 同步同一 upsert 出口）。
// 定价四模式（pricing.go ApplyPricingImport）：pending 不算价不上架（Price=-1
// 不覆盖既有价，运营补价后手动上架）；导入价写入基线（后续同步走价格保护）。
func (s *SyncService) ImportOne(ctx context.Context, conn *ent.SupplyConnection, p *adapter.Product, categoryMap map[string]uint64, mode string, markupPercent float64, markupAmount int64) (bool, error) {
	rule := productPricingRule{Mode: mode, Percent: markupPercent, Amount: markupAmount}
	importPrice := func(upstream int64) int64 { return rule.price(conn, upstream) }
	price := importPrice(p.Price)
	if p.IsActive && mode != PriceModePending && (p.Price <= 0 || price <= 0) {
		return false, fmt.Errorf("商品 %s 报价无效，未导入", p.ID)
	}
	status := int8(1)
	writePrice := price
	if mode == PriceModePending {
		status = 0 // 待定价：导入后不上架
		writePrice = -1
	}
	if !p.IsActive {
		status = 0 // 上游不可售品导入即下架
	}
	// 既有映射（保留锁定或受保护的封面；新导入为 NotFound）
	mapping, merr := s.repo.GetMapping(ctx, conn.ID, p.ID, "")
	if merr != nil && merr != ErrNotFound {
		return false, merr
	}
	write := catalogport.UpstreamProductInput{
		ConnectionID:        conn.ID,
		UpstreamProductCode: p.ID,
		UpstreamSyncedAt:    time.Now().UTC(),
		Name:                p.Name,
		Description:         p.Description,
		DescriptionSet:      p.DescriptionSet,
		Cover:               s.coverFor(ctx, mapping, conn, p.Cover), // 上游图采集落本地（fail-open；保留旧文件，避免共享引用失效）
		FactoryPrice:        accountCost(conn, p),
		Status:              status,
		AutoOnshelf:         mode != PriceModePending,
		ReimportDeleted:     true,
		Price:               writePrice,
	}
	if localCat, ok := categoryMap[p.CategoryID]; ok {
		write.CategoryID = localCat
		write.CategorySet = true
	}
	// 手动导入也必须携带规格，上游下单使用 SKU ID。
	for _, sk := range p.SKUs {
		skuPrice := importPrice(sk.Price)
		if p.IsActive && mode != PriceModePending && (sk.Price <= 0 || skuPrice <= 0) {
			return false, fmt.Errorf("商品 %s 规格报价无效，未导入", p.ID)
		}
		if mode == PriceModePending {
			skuPrice = -1 // 待定价时保留已有规格价格，不写零元规格。
		}
		write.SKUs = append(write.SKUs, catalogport.UpstreamSKUInput{
			Code: sk.Code, Name: sk.Name, SpecValues: sk.SpecValues,
			PriceCents: skuPrice,
		})
	}
	// 商品、规格、映射一起提交，失败时保留原归档的同步保护。
	created := false
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		if err := s.checkPricingConnection(ctx, conn); err != nil {
			return err
		}
		productID, wasCreated, err := s.writeProductCategory(ctx, p.CategoryID, &write)
		if err != nil {
			return err
		}
		created = wasCreated
		// 映射 upsert（价格基线：导入价；后续同步据此做运营改价保护）
		override := map[string]any{"rule": rule}
		skuPrices := map[string]any{}
		for _, sk := range write.SKUs {
			if sk.PriceCents >= 0 {
				skuPrices[sk.Code] = sk.PriceCents
			}
		}
		override["sku_prices"] = skuPrices
		if price > 0 {
			override["last_synced_price"] = price
		}
		mapping, err = s.repo.GetMapping(ctx, conn.ID, p.ID, "")
		if err != nil {
			if err != ErrNotFound {
				return err
			}
			mapping = &ent.SupplyMapping{ConnectionID: conn.ID, UpstreamProduct: p.ID}
		}
		mapping.LocalProductID = productID
		mapping.UpstreamCategory = p.CategoryID
		if write.CategorySet {
			mapping.LocalCategoryID = write.CategoryID
		}
		mapping.UpStock = p.Stock
		mapping.StockCheckedAt = stockObservationTime(p)
		mapping.PricingOverride = override
		if err := s.saveProductMapping(ctx, mapping); err != nil {
			return err
		}
		return nil
	})
	return created, err
}

// ── 失败自动重试（ 补强：上游暂不可用 15→30→60s 递进恢复）──
//
// 与请求级 AIMD（pacer）互补：pacer 管单请求间隔（防封 IP），这里管任务整体的
// 暂时性失败恢复——重试计数进程内（重启丢失可接受：定时调度周期兜底重跑）。
// 可重试：上游拉取/单品失败/心跳等暂时性错误；配置类（凭据/参数/限流冷却/
// 对账护栏/取消）不重试。

var syncRetryIntervals = []time.Duration{15 * time.Second, 30 * time.Second, 60 * time.Second}

var syncRetryTracker = struct {
	sync.Mutex
	m map[uint64]int
}{m: map[uint64]int{}}

// retryableSyncCode 可重试错误码判定。
func retryableSyncCode(code string) bool {
	switch code {
	case "LIST_PRODUCTS_FAILED", "SYNC_ITEM_FAILED", "HEARTBEAT_FAILED", "PRICE_QUOTE_FAILED":
		return true
	}
	return false
}

// failAndMaybeRetry 任务失败落终态；可重试错误安排自动重试（error_context 追加
// 可见提示），重试耗尽或不可重试则保持 failed。
func (s *SyncService) failAndMaybeRetry(ctx context.Context, taskID uint64, connectionID uint64, code, errContext string) {
	syncRetryTracker.Lock()
	n := syncRetryTracker.m[taskID]
	if !retryableSyncCode(code) || n >= len(syncRetryIntervals) {
		if retryableSyncCode(code) {
			delete(syncRetryTracker.m, taskID) // 耗尽清理
		}
		syncRetryTracker.Unlock()
		_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, code, errContext)
		return
	}
	delay := syncRetryIntervals[n]
	syncRetryTracker.m[taskID] = n + 1
	syncRetryTracker.Unlock()

	hint := fmt.Sprintf("（%s 后自动重试 %d/%d）", delay, n+1, len(syncRetryIntervals))
	_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, code, errContext+hint)
	s.log.Info("supply.sync.retry_scheduled", "task_id", taskID, "attempt", n+1, "delay", delay.String())

	go func() {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return // 宿主 ctx 结束（进程关停）
		}
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
		defer cancel()
		// 重跑前检查取消意图（等待期被用户取消则不再复活）
		if t, err := s.repo.GetSyncTask(runCtx, taskID); err != nil || !t.CancelRequestedAt.IsZero() {
			return
		}
		if err := s.repo.ResetTaskPending(runCtx, taskID); err != nil {
			return
		}
		if err := s.RunSync(runCtx, taskID); err != nil {
			s.log.Warn("supply.sync.retry_run_failed", "task_id", taskID, "err", err)
		}
	}()
}

// clearSyncRetry 任务成功终态清理重试计数。
func clearSyncRetry(taskID uint64) {
	syncRetryTracker.Lock()
	delete(syncRetryTracker.m, taskID)
	syncRetryTracker.Unlock()
}
