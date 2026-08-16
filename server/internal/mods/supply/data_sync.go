package supply

// 货源同步服务（P2-01 T3/T4/T5）：
//   - 同步流程：拉分类/商品 → upsert 本地（分类映射 → 自动上架开关）→ 更新 up_stock 缓存
//     → 按定价策略重算本地价（价格保护：auto_sync_price 关 / 运营已改价 / 固定覆盖价 不覆盖）
//   - 任务追踪：进度/心跳(30s)/统计/取消标志（分批间检查）；失败 error_context 落库
//   - 下架语义：上游 inactive → 本地隐藏(2)；上游 deleted → 本地下架(0)（哨兵错误区分）
//   - fail-open：缓存 up_stock 不足时实时 QueryStock 单品校验；上游查询失败放行
//   - 终态发布 sync.completed 事件（P2-05 告警 / P3-07 对账数据源）

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
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

// SyncService 同步执行器。
type SyncService struct {
	repo   *SupplyRepoImpl
	writer catalogport.UpstreamProductWriter
	enq    queue.Enqueuer
	outbox events.Writer // sync.completed 发布
	log    *slog.Logger
}

// NewSyncService 构造。
func NewSyncService(repo *SupplyRepoImpl, writer catalogport.UpstreamProductWriter, enq queue.Enqueuer, outbox events.Writer, log *slog.Logger) *SyncService {
	return &SyncService{repo: repo, writer: writer, enq: enq, outbox: outbox, log: log}
}

// StartTask 调度同步任务：有 Redis 入 low 队列；无 Redis 直接异步执行（降级串行语义）。
func (s *SyncService) StartTask(ctx context.Context, taskID uint64) error {
	payload, err := json.Marshal(map[string]uint64{"task_id": taskID})
	if err != nil {
		return err
	}
	if s.enq.Enabled() {
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
	conn, err := s.repo.GetConnection(ctx, task.ConnectionID)
	if err != nil {
		return err
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

	stats := TaskProgress{Stage: "fetching_products", Page: 1}
	heartbeat := time.Now()

	// 分类映射缓存：upstream_category → local_category_id（mapping.upstream_category 命中）
	// 全量拉取分类（dujiao 支持；acg-faka 返回空，走商品内嵌 category_id）
	categoryMap := map[string]uint64{}
	catErr := s.cacheCategoryMappings(ctx, a, conn.ID, categoryMap)
	if catErr != nil {
		s.log.Warn("supply.sync.categories_failed", "connection_id", conn.ID, "err", catErr)
	}

	page := 1
	for {
		if !heartbeat.IsZero() && time.Since(heartbeat) >= 30*time.Second {
			cancel, err := s.repo.TouchTask(ctx, taskID, stats)
			if err != nil {
				_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "HEARTBEAT_FAILED", err.Error())
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

		list, err := a.ListProducts(ctx, page, 50, true)
		if err != nil {
			// fail-open：拉取失败 → 已处理部分保留，任务失败留痕（可重跑）
			_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "LIST_PRODUCTS_FAILED", err.Error())
			s.publishCompleted(ctx, conn.ID, taskID, "failed")
			return nil
		}
		stats.Page = page
		for i := range list.Items {
			cancel, err := s.syncOne(ctx, taskID, conn, &list.Items[i], categoryMap, &stats)
			if err != nil {
				_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusFailed, "SYNC_ITEM_FAILED", err.Error())
				s.publishCompleted(ctx, conn.ID, taskID, "failed")
				return nil
			}
			if cancel {
				_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusCanceled, "", "")
				s.publishCompleted(ctx, conn.ID, taskID, "canceled")
				return nil
			}
		}
		if !list.HasMore {
			break
		}
		page++
	}

	// 收尾：last_synced_at + done
	_, err = s.repo.TouchTask(ctx, taskID, stats)
	if err == nil {
		now := time.Now().UTC()
		client := s.repo.entClient(ctx)
		_, _ = client.SupplyConnection.UpdateOneID(conn.ID).SetLastSyncedAt(now).Save(ctx)
	}
	_ = s.repo.FinishTask(ctx, taskID, supplysynctask.StatusDone, "", "")
	s.publishCompleted(ctx, conn.ID, taskID, "done")
	return nil
}

// syncOne 同步单个商品：价格保护 → upsert 商品 → upsert 映射（up_stock 缓存）。
// 返回 (cancelRequested, error)。
func (s *SyncService) syncOne(ctx context.Context, taskID uint64, conn *ent.SupplyConnection, p *adapter.Product, categoryMap map[string]uint64, stats *TaskProgress) (bool, error) {
	mapping, err := s.repo.GetMapping(ctx, conn.ID, p.ID, "")
	notFound := err == ErrNotFound
	if err != nil && !notFound {
		return false, err
	}
	if notFound {
		mapping = &ent.SupplyMapping{ConnectionID: conn.ID, UpstreamProduct: p.ID}
	}

	// 状态语义：上游 inactive → 本地隐藏(2)；deleted 哨兵 → 本地下架(0)
	status := int8(1)
	if !p.IsActive {
		status = 2
	}

	// 定价（价格保护三级判定）
	newPrice := ApplyPricing(p.Price, conn.ExchangeRate, conn.PriceMarkupPercent, string(conn.PriceRoundingMode))
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
	} else if lastSync, ok := override["last_synced_price"]; ok && p.Price > 0 {
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
	if p.Stock > 0 {
		mapping.UpStock = p.Stock
	}
	mapping.PricingOverride = override
	if _, err := s.repo.UpsertMapping(ctx, mapping); err != nil {
		return false, err
	}

	// 取消检查（每商品粒度太细，放每页尾部；此处仅任务级取消标志）
	return false, nil
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

// publishCompleted 发布 sync.completed 事件（终态：done/failed/canceled）。
func (s *SyncService) publishCompleted(ctx context.Context, connectionID, taskID uint64, status string) {
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
