package supply

// 货源同步服务（P2-01 T3/T4/T5 + P2-10 S1 同步引擎改造）：
//   - 三类 scope：collect（采集：upsert + 库存 + 删除对账）/ price（仅刷价格）/
//     status（仅刷上下架 + up_stock）——轻量 scope 走 maintainer 端口不建不删
//   - 增量同步：驱动实现 adapter.IncrementalLister 时按锚点拉取变更
//     （锚点存 settings.sync_anchors，-1 分钟安全窗）；否则自动回落全量
//   - 删除对账：仅权威快照（全量 + IncludesInactive 回声）做——seenCodes 对账
//     把上游已消失商品批量下架；护栏：上游声称 total > 实际处理数 → 任务失败
//     不删（宁可保守，1.x「不能批量误删」纪律）
//   - 请求节流：分页页间 request_delay（settings.schedule，防上游限流封 IP）；
//     库存补查分批并发 + 批次间隔 + 600s 限速预算（1.x AcgFakaDriver 同款参数）
//   - 任务追踪：进度/心跳(30s)/统计/取消标志；失败 error_context 落库
//   - fail-open：库存补查失败项保持 -1（无限语义）仅告警；上游查询失败放行
//   - 终态发布 sync.completed 事件（P2-05 告警 / P3-07 对账数据源）

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplysynctask"
)

// SyncTaskType 同步任务队列类型（asynq mux 精确匹配；low 队列）。
const SyncTaskType = "supply.sync"

// 同步 scope（任务粒度；空 = collect 兼容历史任务）。
const (
	ScopeCollect = "collect"
	ScopePrice   = "price"
	ScopeStatus  = "status"
)

// 节流/补查默认参数（settings.schedule 可覆盖；对齐 1.x AcgFakaDriver）。
const (
	defaultPageDelaySec   = 1    // 商品分页页间间隔（秒；0=不限）
	defaultStockConc      = 3    // 库存补查并发（1-10）
	defaultStockBatchMs   = 200  // 补查批次间隔（毫秒）
	stockThrottleBudgetMs = 600_000 // 补查限速预算（600s，超出报错提示调参）
)

// scheduleSettings 连接级节流参数（P2-10 §5.2；S2 自适应节奏器在此基础上倍增）。
type scheduleSettings struct {
	PageDelay      time.Duration // 商品分页页间隔
	StockConc      int           // 库存补查并发
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
	pacer      *Pacer                                 // 自适应节奏器（nil=静态节流，测试用）
	enq        queue.Enqueuer
	outbox     events.Writer // sync.completed 发布
	log        *slog.Logger
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
	if scope != ScopeCollect && scope != ScopePrice && scope != ScopeStatus {
		_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "INVALID_SCOPE", "scope 必须为 collect|price|status")
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

	// 列表函数解析：增量（驱动支持 + 有锚点）→ 全量回落。
	// 增量快照不具对账权威性（未见 ≠ 已删除）→ authoritative 仅在全量且回声完整时成立。
	list, incremental := resolveLister(a, task, readSyncAnchor(conn, scope), s.log)
	return s.runLoop(ctx, taskID, task, conn, a, sched, scope, incremental, list)
}

// resolveLister 列表函数决策（collect/price 共用）：
// 驱动实现 IncrementalLister 且锚点有效 → 增量（含下架变更）；否则全量。
func resolveLister(a adapter.Adapter, task *ent.SupplySyncTask, anchor time.Time, log *slog.Logger) (func(ctx context.Context, page, pageSize int) (*adapter.ProductList, error), bool) {
	if task.Mode != "incremental" || anchor.IsZero() {
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
		if err := s.cacheCategoryMappings(ctx, a, conn.ID, categoryMap); err != nil {
			s.log.Warn("supply.sync.categories_failed", "connection_id", conn.ID, "err", err)
		}
	}

	// 对账状态：seenCodes（权威快照时收集）；reportedTotal 护栏基准
	authoritative := !incremental
	seen := map[string]bool{}
	reportedTotal := 0
	processed := 0

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

		// 库存补查（仅 collect：列表缺库存的项分批查实时值；失败项保持 -1 放行）
		if scope == ScopeCollect {
			if err := s.backfillStocks(ctx, a, sched, list0.Items, taskID); err != nil {
				_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "STOCK_BACKFILL_BUDGET", err.Error())
				s.publishCompleted(ctx, conn.ID, taskID, "failed")
				return nil
			}
		}

		for i := range list0.Items {
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
	_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusDone, "", "")
	clearSyncRetry(taskID)
	s.publishCompleted(ctx, conn.ID, taskID, "done")
	return nil
}

// syncOne 同步单个商品（scope 分支）：
//   - collect：价格保护 → upsert 商品 → upsert 映射（up_stock 缓存）
//   - price：仅已映射商品刷价格（价格保护同口径；不建不删不动库存）
//   - status：仅已映射商品刷上下架 + up_stock（不建不删不动价格）
//
// 返回 (cancelRequested, error)。
func (s *SyncService) syncOne(ctx context.Context, taskID uint64, task *ent.SupplySyncTask, conn *ent.SupplyConnection, p *adapter.Product, categoryMap map[string]uint64, stats *TaskProgress) (bool, error) {
	mapping, err := s.repo.GetMapping(ctx, conn.ID, p.ID, "")
	notFound := err == ErrNotFound
	if err != nil && !notFound {
		return false, err
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

	// 状态语义：上游 inactive → 本地隐藏(2)；deleted 哨兵 → 本地下架(0)
	status := int8(1)
	if !p.IsActive {
		status = 2
	}

	// 定价（价格保护三级判定；force_reprice 覆盖运营改价保护）
	newPrice := ApplyPricing(p.Price, conn.ExchangeRate, conn.PriceMarkupPercent, conn.PriceMarkupAmount, string(conn.PriceRoundingMode))
	priceToWrite, writePrice, priceUpdated, override := s.resolvePrice(ctx, conn, mapping, p.Price, newPrice, task.ForceReprice)

	// upsert 本地商品（价格 -1 = 不更新）
	write := catalogport.UpstreamProductInput{
		ConnectionID:        conn.ID,
		UpstreamProductCode: p.ID,
		UpstreamSyncedAt:    time.Now().UTC(),
		Name:                p.Name,
		Description:         p.Description,
		Cover:               p.Cover,
		FactoryPrice:        p.FactoryPrice,
		Status:              status,
		AutoOnshelf:         autoOnshelf(conn.Settings),
	}
	if localCat, ok := categoryMap[p.CategoryID]; ok {
		write.CategoryID = localCat
	}
	if !writePrice {
		write.Price = -1
		stats.ManualSkipped++
	} else {
		write.Price = priceToWrite
		if priceUpdated {
			stats.PriceUpdated++
		}
	}
	productID, created, err := s.writer.UpsertUpstreamProduct(ctx, write)
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
	mapping.UpStock = p.Stock
	mapping.PricingOverride = override
	if _, err := s.repo.UpsertMapping(ctx, mapping); err != nil {
		return false, err
	}

	// 取消检查（每商品粒度太细，放每页尾部；此处仅任务级取消标志）
	return false, nil
}

// syncPriceOnly price scope 轻路径：价格保护 → 仅更新价格 + 基线持久化。
func (s *SyncService) syncPriceOnly(ctx context.Context, conn *ent.SupplyConnection, mapping *ent.SupplyMapping, p *adapter.Product, force bool, stats *TaskProgress) error {
	newPrice := ApplyPricing(p.Price, conn.ExchangeRate, conn.PriceMarkupPercent, conn.PriceMarkupAmount, string(conn.PriceRoundingMode))
	priceToWrite, writePrice, priceUpdated, override := s.resolvePrice(ctx, conn, mapping, p.Price, newPrice, force)
	if writePrice && s.maintainer != nil {
		if _, err := s.maintainer.UpdateUpstreamPrice(ctx, conn.ID, p.ID, priceToWrite); err != nil {
			return err
		}
		if priceUpdated {
			stats.PriceUpdated++
		}
	} else {
		stats.ManualSkipped++
	}
	mapping.PricingOverride = override
	if _, err := s.repo.UpsertMapping(ctx, mapping); err != nil {
		return err
	}
	stats.Processed++
	return nil
}

// syncStatusOnly status scope 轻路径：上下架状态 + up_stock 缓存（-1 保留原值）。
func (s *SyncService) syncStatusOnly(ctx context.Context, conn *ent.SupplyConnection, mapping *ent.SupplyMapping, p *adapter.Product, stats *TaskProgress) error {
	status := int8(1)
	if !p.IsActive {
		status = 2
	}
	if s.maintainer != nil {
		if _, err := s.maintainer.UpdateUpstreamStatus(ctx, conn.ID, p.ID, status); err != nil {
			return err
		}
	}
	if p.Stock >= 0 {
		mapping.UpStock = p.Stock
	}
	if _, err := s.repo.UpsertMapping(ctx, mapping); err != nil {
		return err
	}
	stats.Updated++
	stats.Processed++
	return nil
}

// resolvePrice 价格保护三级判定（collect/price 共用口径）：
//  1. auto_sync_price=false → 不写（运营手工定价域）
//  2. pricing_override.price 固定覆盖价 → 恒用固定价
//  3. 本地当前价 ≠ last_synced_price 基线 → 运营改过价 → 保护；基线更新为运营价
//     （force=true 时跳过本条——管理员强制重价）
//
// 返回 (写入价, 是否写价, 是否计 price_updated, 更新后的 override)。
func (s *SyncService) resolvePrice(ctx context.Context, conn *ent.SupplyConnection, mapping *ent.SupplyMapping, upstreamPrice, newPrice int64, force bool) (int64, bool, bool, map[string]any) {
	priceToWrite := newPrice
	writePrice := true
	priceUpdated := false

	override := mapping.PricingOverride
	if override == nil {
		override = map[string]any{}
	}
	if !conn.AutoSyncPrice {
		writePrice = false // 关自动同步：运营手工定价，同步永不覆盖
	} else if fixed, ok := override["price"]; ok {
		priceToWrite = toInt64(fixed) // 固定覆盖价
		priceUpdated = true
	} else if lastSync, ok := override["last_synced_price"]; ok && upstreamPrice > 0 && !force {
		// 本地当前价 != 上次同步价 → 运营改过价 → 保护
		current, err := s.currentProductPrice(ctx, mapping.LocalProductID)
		if err == nil && current != toInt64(lastSync) && current != newPrice {
			writePrice = false
			override["last_synced_price"] = current // 基线更新为运营价（后续同步保持）
		}
	}
	if writePrice && !priceUpdated {
		// 记录新基线（价格更新或首次同步都算 price_updated）
		priceUpdated = newPrice > 0
		override["last_synced_price"] = newPrice
	}
	return priceToWrite, writePrice, priceUpdated, override
}

// backfillStocks 库存补查（collect scope；列表缺库存（-1）的项分批并发查实时值）。
// 批次间隔节流 + 600s 预算护栏（超限报错提示调参，1.x 同款）；单项失败保持 -1
// 放行（fail-open；传输层已内建网络错误/5xx 重试，此处不叠加外层重试）。
func (s *SyncService) backfillStocks(ctx context.Context, a adapter.Adapter, cfg scheduleSettings, items []adapter.Product, taskID uint64) error {
	var missing []int
	for i := range items {
		if items[i].Stock == -1 {
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
	for ci := 0; ci < chunks; ci++ {
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
				st, err := a.GetStock(ctx, items[i].ID, "")
				if err != nil {
					s.log.Warn("supply.sync.stock_backfill_failed", "task_id", taskID, "code", items[i].ID, "err", err)
					return // fail-open：保持 -1
				}
				if st >= 0 {
					items[i].Stock = st
				}
			}(i)
		}
		wg.Wait()
		if ci < chunks-1 {
			if err := sleepCtx(ctx, cfg.StockBatchDelay); err != nil {
				return err
			}
		}
	}
	return nil
}

// cacheCategoryMappings 构建 上游分类标识 → 本地分类 id 映射
// （mapping.upstream_category 记录了运营手工映射；无映射分类不自动建）。
func (s *SyncService) cacheCategoryMappings(ctx context.Context, a adapter.Adapter, connectionID uint64, out map[string]uint64) error {
	cats, err := a.ListCategories(ctx)
	if err != nil {
		return err
	}
	_ = cats
	// 本地分类映射表：扫描本连接全部 mapping，收集 upstream_category → local_category_id
	ms, _, err := s.repo.ListMappings(ctx, connectionID, 1, 100000)
	if err != nil {
		return err
	}
	for _, m := range ms {
		if m.LocalCategoryID > 0 && m.UpstreamCategory != "" {
			out[m.UpstreamCategory] = m.LocalCategoryID
		}
	}
	return nil
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

// ImportOne 单品导入（P2-10 D 交互式导入；与 collect 同步同一 upsert 出口）。
// 定价四模式（pricing.go ApplyPricingImport）：pending 不算价不上架（Price=-1
// 不覆盖既有价，运营补价后手动上架）；导入价写入基线（后续同步走价格保护）。
func (s *SyncService) ImportOne(ctx context.Context, conn *ent.SupplyConnection, p *adapter.Product, categoryMap map[string]uint64, mode string, markupPercent float64, markupAmount int64) (bool, error) {
	price := ApplyPricingImport(p.Price, conn.ExchangeRate, markupPercent, markupAmount, mode, string(conn.PriceRoundingMode))
	status := int8(1)
	writePrice := price
	if mode == PriceModePending {
		status = 0 // 待定价：导入后不上架
		writePrice = -1
	}
	write := catalogport.UpstreamProductInput{
		ConnectionID:        conn.ID,
		UpstreamProductCode: p.ID,
		UpstreamSyncedAt:    time.Now().UTC(),
		Name:                p.Name,
		Description:         p.Description,
		Cover:               p.Cover,
		FactoryPrice:        p.FactoryPrice,
		Status:              status,
		AutoOnshelf:         mode != PriceModePending,
		Price:               writePrice,
	}
	if localCat, ok := categoryMap[p.CategoryID]; ok {
		write.CategoryID = localCat
	}
	productID, created, err := s.writer.UpsertUpstreamProduct(ctx, write)
	if err != nil {
		return false, err
	}
	// 映射 upsert（价格基线：导入价；后续同步据此做运营改价保护）
	override := map[string]any{}
	if price > 0 {
		override["last_synced_price"] = price
	}
	mapping, err := s.repo.GetMapping(ctx, conn.ID, p.ID, "")
	if err != nil {
		if err != ErrNotFound {
			return created, err
		}
		mapping = &ent.SupplyMapping{ConnectionID: conn.ID, UpstreamProduct: p.ID}
	}
	mapping.LocalProductID = productID
	mapping.UpstreamCategory = p.CategoryID
	mapping.UpStock = p.Stock
	mapping.PricingOverride = override
	if _, err := s.repo.UpsertMapping(ctx, mapping); err != nil {
		return created, err
	}
	return created, nil
}

// ── 失败自动重试（P2-10 补强：上游暂不可用 15→30→60s 递进恢复）──
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
	case "LIST_PRODUCTS_FAILED", "SYNC_ITEM_FAILED", "HEARTBEAT_FAILED":
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
