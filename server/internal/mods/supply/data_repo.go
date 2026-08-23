package supply

// 货源连接仓储（P2-01）：连接 CRUD（凭据 AES-GCM 加解密）、映射规则 CRUD、
// 同步任务 CRUD（进度/心跳/取消）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplysynctask"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

// ErrNotFound 记录不存在（404 语义）。
var ErrNotFound = errors.New("supply: 记录不存在")

// ErrHasMappings 连接存在映射，禁止删除。
var ErrHasMappings = errors.New("supply: 连接存在映射，禁止删除")

// SupplyRepoImpl 货源仓储实现。
type SupplyRepoImpl struct {
	data *data.Data
	box  *crypto.Box // ZCARD_DATA_KEY（凭据加密）
}

// NewSupplyRepoImpl 构造。
func NewSupplyRepoImpl(d *data.Data, box *crypto.Box) *SupplyRepoImpl {
	return &SupplyRepoImpl{data: d, box: box}
}

// entClient 取 ent client（同步服务等非 data*.go 文件经此访问；事务上下文自动携带）。
func (r *SupplyRepoImpl) entClient(ctx context.Context) *ent.Client {
	return data.Client(ctx, r.data)
}

// credAAD 凭据加密 AAD（按连接稳定标识绑定，防密文换库）。
func credAAD(driver string, baseURL string) []byte {
	return []byte("supply_connection:" + driver + ":" + baseURL)
}

// SealCredentials 加密凭据 JSON。
func (r *SupplyRepoImpl) SealCredentials(driver, baseURL, credsJSON string) ([]byte, error) {
	enc, err := r.box.Seal([]byte(credsJSON), credAAD(driver, baseURL))
	if err != nil {
		return nil, fmt.Errorf("supply: 凭据加密失败: %w", err)
	}
	return enc, nil
}

// OpenCredentials 解密凭据 JSON（失败 → 提示重配，列表永不因此 500）。
func (r *SupplyRepoImpl) OpenCredentials(conn *ent.SupplyConnection) (string, error) {
	plain, err := r.box.Open(conn.Credentials, credAAD(conn.Driver, conn.BaseURL))
	if err != nil {
		return "", fmt.Errorf("supply: 凭据解密失败（请重新配置连接）: %w", err)
	}
	return string(plain), nil
}

// ── 连接 CRUD ──────────────────────────────────────────────

// CreateConnection 创建连接（base_url SSRF 校验在 service 层）。
func (r *SupplyRepoImpl) CreateConnection(ctx context.Context, conn *ent.SupplyConnection) (*ent.SupplyConnection, error) {
	return data.Client(ctx, r.data).SupplyConnection.Create().
		SetName(conn.Name).
		SetDriver(conn.Driver).
		SetBaseURL(conn.BaseURL).
		SetCredentials(conn.Credentials).
		SetStatus(supplyconnection.Status(conn.Status)).
		SetCallbackURL(conn.CallbackURL).
		SetRetryMax(conn.RetryMax).
		SetRetryIntervals(conn.RetryIntervals).
		SetExchangeRate(conn.ExchangeRate).
		SetPriceMarkupPercent(conn.PriceMarkupPercent).
		SetPriceMarkupAmount(conn.PriceMarkupAmount).
		SetPriceRoundingMode(supplyconnection.PriceRoundingMode(conn.PriceRoundingMode)).
		SetAutoSyncPrice(conn.AutoSyncPrice).
		SetStockMode(supplyconnection.StockMode(conn.StockMode)).
		SetSettings(conn.Settings).
		Save(ctx)
}

// UpdateConnection 更新连接（nil 字段不更新语义由 service 层拼装；凭据单独处理）。
func (r *SupplyRepoImpl) UpdateConnection(ctx context.Context, id uint64, upd *ent.SupplyConnection) (*ent.SupplyConnection, error) {
	// 先读原记录（凭据列保护：更新路径不触碰凭据除非显式传入）
	existing, err := r.GetConnection(ctx, id)
	if err != nil {
		return nil, err
	}
	if upd.Name != "" {
		existing.Name = upd.Name
	}
	if upd.BaseURL != "" {
		existing.BaseURL = upd.BaseURL
	}
	if upd.CallbackURL != "" {
		existing.CallbackURL = upd.CallbackURL
	}
	if upd.RetryMax != 0 {
		existing.RetryMax = upd.RetryMax
	}
	if upd.RetryIntervals != "" {
		existing.RetryIntervals = upd.RetryIntervals
	}
	if upd.ExchangeRate != 0 {
		existing.ExchangeRate = upd.ExchangeRate
	}
	if upd.PriceMarkupPercent != 0 {
		existing.PriceMarkupPercent = upd.PriceMarkupPercent
	}
	if upd.PriceMarkupAmount != 0 {
		existing.PriceMarkupAmount = upd.PriceMarkupAmount
	}
	if upd.PriceRoundingMode != "" {
		existing.PriceRoundingMode = upd.PriceRoundingMode
	}
	if upd.StockMode != "" {
		existing.StockMode = upd.StockMode
	}
	if upd.Status != "" {
		existing.Status = upd.Status
	}
	if upd.AutoSyncPrice {
		existing.AutoSyncPrice = true
	}
	if upd.Settings != nil {
		existing.Settings = upd.Settings
	}
	return data.Client(ctx, r.data).SupplyConnection.UpdateOneID(id).
		SetName(existing.Name).
		SetBaseURL(existing.BaseURL).
		SetCallbackURL(existing.CallbackURL).
		SetRetryMax(existing.RetryMax).
		SetRetryIntervals(existing.RetryIntervals).
		SetExchangeRate(existing.ExchangeRate).
		SetPriceMarkupPercent(existing.PriceMarkupPercent).
		SetPriceMarkupAmount(existing.PriceMarkupAmount).
		SetPriceRoundingMode(existing.PriceRoundingMode).
		SetAutoSyncPrice(existing.AutoSyncPrice).
		SetStockMode(existing.StockMode).
		SetStatus(existing.Status).
		SetSettings(existing.Settings).
		Save(ctx)
}

// UpdateCredentials 更新凭据（单独入口，避免全量更新误写密文）。
func (r *SupplyRepoImpl) UpdateCredentials(ctx context.Context, id uint64, driver, baseURL, credsJSON string) error {
	enc, err := r.SealCredentials(driver, baseURL, credsJSON)
	if err != nil {
		return err
	}
	_, err = data.Client(ctx, r.data).SupplyConnection.UpdateOneID(id).SetCredentials(enc).Save(ctx)
	return err
}

// DeleteConnection 删除连接（存在映射时拒绝）。
func (r *SupplyRepoImpl) DeleteConnection(ctx context.Context, id uint64) error {
	n, err := data.Client(ctx, r.data).SupplyMapping.Query().
		Where(supplymapping.ConnectionID(id)).
		Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrHasMappings
	}
	err = data.Client(ctx, r.data).SupplyConnection.DeleteOneID(id).Exec(ctx)
	if err != nil && ent.IsNotFound(err) {
		return ErrNotFound
	}
	return err
}

// GetConnection 连接详情（含凭据密文列，调用方注意脱敏）。
func (r *SupplyRepoImpl) GetConnection(ctx context.Context, id uint64) (*ent.SupplyConnection, error) {
	conn, err := data.Client(ctx, r.data).SupplyConnection.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return conn, nil
}

// ListConnections 连接列表。
func (r *SupplyRepoImpl) ListConnections(ctx context.Context, page, pageSize int) ([]*ent.SupplyConnection, int, error) {
	q := data.Client(ctx, r.data).SupplyConnection.Query().Order(ent.Desc(supplyconnection.FieldID))
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	conns, err := q.Offset((page - 1) * pageSize).Limit(pageSize).All(ctx)
	return conns, total, err
}

// ListActiveConnections 全部 active 连接（探活/巡检用）。
func (r *SupplyRepoImpl) ListActiveConnections(ctx context.Context) ([]*ent.SupplyConnection, error) {
	return data.Client(ctx, r.data).SupplyConnection.Query().
		Where(supplyconnection.StatusEQ(supplyconnection.StatusActive)).
		Order(ent.Asc(supplyconnection.FieldID)).
		All(ctx)
}

// UpdatePingResult 记录探活结果（含 settings 内累计统计：成功率/平均延迟）。
func (r *SupplyRepoImpl) UpdatePingResult(ctx context.Context, id uint64, ok bool, latencyMs int64, balance int64, errMsg string) error {
	conn, err := r.GetConnection(ctx, id)
	if err != nil {
		return err
	}
	settings := conn.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	hist, _ := settings["ping_history"].(map[string]any)
	if hist == nil {
		hist = map[string]any{"ok": int64(0), "fail": int64(0), "total_latency_ms": int64(0)}
	}
	if ok {
		hist["ok"] = toInt64(hist["ok"]) + 1
	} else {
		hist["fail"] = toInt64(hist["fail"]) + 1
	}
	hist["total_latency_ms"] = toInt64(hist["total_latency_ms"]) + latencyMs
	settings["ping_history"] = hist

	now := time.Now().UTC()
	upd := data.Client(ctx, r.data).SupplyConnection.UpdateOneID(id).
		SetLastPingAt(now).
		SetLastPingOk(ok).
		SetSettings(settings)
	if ok {
		upd.SetBalanceCache(balance)
		upd.ClearLastError()
	} else if errMsg != "" {
		upd.SetLastError(errMsg)
	}
	_, err = upd.Save(ctx)
	return err
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	case int:
		return int64(x)
	}
	return 0
}

// ── 映射 CRUD ──────────────────────────────────────────────

// UpsertMapping 创建或更新映射（单语句 ON CONFLICT，命中
// UNIQUE(connection_id, upstream_product, upstream_sku)；旧实现先查后写
// 2 次往返且并发可重复创建——PG/MySQL 走原生 upsert 消除竞态。
// 同步热路径调用（每商品一次），只回报 error；需要实体由调用方回读。
func (r *SupplyRepoImpl) UpsertMapping(ctx context.Context, m *ent.SupplyMapping) error {
	return data.Client(ctx, r.data).SupplyMapping.Create().
		SetConnectionID(m.ConnectionID).
		SetUpstreamCategory(m.UpstreamCategory).
		SetLocalCategoryID(m.LocalCategoryID).
		SetUpstreamProduct(m.UpstreamProduct).
		SetLocalProductID(m.LocalProductID).
		SetUpstreamSku(m.UpstreamSku).
		SetLocalSkuID(m.LocalSkuID).
		SetUpStock(m.UpStock).
		SetPricingOverride(m.PricingOverride).
		OnConflict(
			entsql.ConflictColumns(
				supplymapping.FieldConnectionID,
				supplymapping.FieldUpstreamProduct,
				supplymapping.FieldUpstreamSku,
			),
			// 冲突时全列取新值（含 TimeMixin 的 updated_at 提议值）
			entsql.ResolveWithNewValues(),
		).
		Exec(ctx)
}

// ListMappings 映射列表（按连接过滤）。
func (r *SupplyRepoImpl) ListMappings(ctx context.Context, connectionID uint64, page, pageSize int) ([]*ent.SupplyMapping, int, error) {
	q := data.Client(ctx, r.data).SupplyMapping.Query().Order(ent.Desc(supplymapping.FieldID))
	if connectionID > 0 {
		q = q.Where(supplymapping.ConnectionID(connectionID))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	ms, err := q.Offset((page - 1) * pageSize).Limit(pageSize).All(ctx)
	return ms, total, err
}

// DeleteMapping 删除映射。
func (r *SupplyRepoImpl) DeleteMapping(ctx context.Context, id uint64) error {
	err := data.Client(ctx, r.data).SupplyMapping.DeleteOneID(id).Exec(ctx)
	if err != nil && ent.IsNotFound(err) {
		return ErrNotFound
	}
	return err
}

// GetMapping 按上游键查映射（同步 upsert 判据）。
func (r *SupplyRepoImpl) GetMapping(ctx context.Context, connectionID uint64, upstreamProduct, upstreamSku string) (*ent.SupplyMapping, error) {
	m, err := data.Client(ctx, r.data).SupplyMapping.Query().
		Where(
			supplymapping.ConnectionID(connectionID),
			supplymapping.UpstreamProductEQ(upstreamProduct),
			supplymapping.UpstreamSkuEQ(upstreamSku),
		).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

// ── 节奏器状态（P2-10 S2）────────────────────────────────

// UpdateRateState 仅写节奏器持久状态（rate_state JSON）。
func (r *SupplyRepoImpl) UpdateRateState(ctx context.Context, id uint64, state map[string]any) error {
	_, err := data.Client(ctx, r.data).SupplyConnection.UpdateOneID(id).
		SetRateState(state).Save(ctx)
	return err
}

// TripRateLimit 熔断：冷却截止 + 状态 + last_error。
func (r *SupplyRepoImpl) TripRateLimit(ctx context.Context, id uint64, until time.Time, state map[string]any, lastErr string) error {
	upd := data.Client(ctx, r.data).SupplyConnection.UpdateOneID(id).
		SetRateLimitUntil(until).
		SetRateState(state)
	if lastErr != "" {
		upd.SetLastError(lastErr)
	}
	_, err := upd.Save(ctx)
	return err
}

// ClearRateLimit 解除熔断（半开探测成功后；同时清 last_error）。
func (r *SupplyRepoImpl) ClearRateLimit(ctx context.Context, id uint64) error {
	_, err := data.Client(ctx, r.data).SupplyConnection.UpdateOneID(id).
		ClearRateLimitUntil().
		ClearLastError().
		Save(ctx)
	return err
}

// ── 同步任务 ──────────────────────────────────────────────

// CreateSyncTask 创建同步任务（pending）。
func (r *SupplyRepoImpl) CreateSyncTask(ctx context.Context, connectionID uint64, mode, scope string, forceReprice bool) (*ent.SupplySyncTask, error) {
	return data.Client(ctx, r.data).SupplySyncTask.Create().
		SetConnectionID(connectionID).
		SetMode(mode).
		SetScope(scope).
		SetForceReprice(forceReprice).
		SetStatus(supplysynctask.StatusPending).
		Save(ctx)
}

// GetSyncTask 任务详情。
func (r *SupplyRepoImpl) GetSyncTask(ctx context.Context, id uint64) (*ent.SupplySyncTask, error) {
	t, err := data.Client(ctx, r.data).SupplySyncTask.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return t, nil
}

// ListSyncTasks 任务列表。
func (r *SupplyRepoImpl) ListSyncTasks(ctx context.Context, connectionID uint64, page, pageSize int) ([]*ent.SupplySyncTask, int, error) {
	q := data.Client(ctx, r.data).SupplySyncTask.Query().Order(ent.Desc(supplysynctask.FieldID))
	if connectionID > 0 {
		q = q.Where(supplysynctask.ConnectionID(connectionID))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	ts, err := q.Offset((page - 1) * pageSize).Limit(pageSize).All(ctx)
	return ts, total, err
}

// TaskProgress 同步进度快照（心跳/统计/取消检查的落库单元）。
type TaskProgress struct {
	Stage         string
	Page          int
	Processed     int32
	Created       int32
	Updated       int32
	PriceUpdated  int32
	ManualSkipped int32
	Hidden        int32
	Deleted       int32
}

// TouchTask 心跳 + 进度（cancel_requested_at 已置则返回 true 表示应停止）。
func (r *SupplyRepoImpl) TouchTask(ctx context.Context, id uint64, p TaskProgress) (cancelRequested bool, err error) {
	task, err := r.GetSyncTask(ctx, id)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	upd := data.Client(ctx, r.data).SupplySyncTask.UpdateOneID(id).
		SetHeartbeatAt(now).
		SetProcessedCount(task.ProcessedCount + p.Processed).
		SetCreatedCount(task.CreatedCount + p.Created).
		SetUpdatedCount(task.UpdatedCount + p.Updated).
		SetPriceUpdatedCount(task.PriceUpdatedCount + p.PriceUpdated).
		SetManualSkippedCount(task.ManualSkippedCount + p.ManualSkipped).
		SetHiddenCount(task.HiddenCount + p.Hidden).
		SetDeletedCount(task.DeletedCount + p.Deleted)
	if p.Stage != "" {
		upd.SetCurrentStage(p.Stage)
	}
	if p.Page > 0 {
		upd.SetCurrentPage(int32(p.Page))
	}
	if !task.CancelRequestedAt.IsZero() {
		return true, nil
	}
	_, err = upd.Save(ctx)
	return false, err
}

// SetTaskProcessing 任务开工（pending → processing，记 started_at）。
func (r *SupplyRepoImpl) SetTaskProcessing(ctx context.Context, id uint64, total int32) error {
	_, err := data.Client(ctx, r.data).SupplySyncTask.UpdateOneID(id).
		SetStatus(supplysynctask.StatusProcessing).
		SetStartedAt(time.Now().UTC()).
		SetTotalCount(total).
		Save(ctx)
	return err
}

// FinishTask 任务终态（done/failed/canceled + 统计 + finished_at）。
func (r *SupplyRepoImpl) FinishTask(ctx context.Context, id uint64, status supplysynctask.Status, errorCode, errorContext string) error {
	upd := data.Client(ctx, r.data).SupplySyncTask.UpdateOneID(id).
		SetStatus(status).
		SetFinishedAt(time.Now().UTC())
	if errorCode != "" {
		upd.SetErrorCode(errorCode)
	}
	if errorContext != "" {
		upd.SetErrorContext(errorContext)
	}
	_, err := upd.Save(ctx)
	return err
}

// ResetTaskPending 重置任务为 pending（失败自动重试用；清心跳与取消标志）。
func (r *SupplyRepoImpl) ResetTaskPending(ctx context.Context, id uint64) error {
	_, err := data.Client(ctx, r.data).SupplySyncTask.UpdateOneID(id).
		SetStatus(supplysynctask.StatusPending).
		ClearHeartbeatAt().
		ClearCancelRequestedAt().
		Save(ctx)
	return err
}

// RequestCancel 请求取消（分批间检查标志）。
func (r *SupplyRepoImpl) RequestCancel(ctx context.Context, id uint64) (*ent.SupplySyncTask, error) {
	task, err := r.GetSyncTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if !task.CancelRequestedAt.IsZero() {
		return task, nil
	}
	return data.Client(ctx, r.data).SupplySyncTask.UpdateOneID(id).
		SetCancelRequestedAt(time.Now().UTC()).
		Save(ctx)
}

// LoadTaskProgress 读取任务当前统计（取消检查 + 进度读取）。
func (r *SupplyRepoImpl) LoadTaskProgress(ctx context.Context, id uint64) (*ent.SupplySyncTask, error) {
	return r.GetSyncTask(ctx, id)
}

// ── 调度（P2-10 S3）───────────────────────────────────────

// HasRunningTask 连接是否存在未完结同步任务（pending/processing；调度防重入）。
func (r *SupplyRepoImpl) HasRunningTask(ctx context.Context, connectionID uint64) (bool, error) {
	n, err := data.Client(ctx, r.data).SupplySyncTask.Query().
		Where(
			supplysynctask.ConnectionID(connectionID),
			supplysynctask.StatusIn(supplysynctask.StatusPending, supplysynctask.StatusProcessing),
		).
		Count(ctx)
	return n > 0, err
}

// TouchScopeAnchor 写 scope 调度锚点列（派发定时任务时回写）。
func (r *SupplyRepoImpl) TouchScopeAnchor(ctx context.Context, id uint64, scope string) error {
	now := time.Now().UTC()
	upd := data.Client(ctx, r.data).SupplyConnection.UpdateOneID(id)
	switch scope {
	case ScopeCollect:
		upd.SetLastCollectAt(now)
	case ScopePrice:
		upd.SetLastPriceSyncAt(now)
	case ScopeStatus:
		upd.SetLastStatusSyncAt(now)
	default:
		return nil
	}
	_, err := upd.Save(ctx)
	return err
}

// ListStaleProcessing 心跳超时的 processing 任务（看门狗 reapStale；
// 心跳为零的用 started_at 判定——刚开工尚未打第一次心跳的任务不会误伤）。
func (r *SupplyRepoImpl) ListStaleProcessing(ctx context.Context, staleBefore time.Time) ([]*ent.SupplySyncTask, error) {
	rows, err := data.Client(ctx, r.data).SupplySyncTask.Query().
		Where(supplysynctask.StatusEQ(supplysynctask.StatusProcessing)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*ent.SupplySyncTask, 0, 4)
	for _, t := range rows {
		anchor := t.HeartbeatAt
		if anchor.IsZero() {
			anchor = t.StartedAt
		}
		if !anchor.IsZero() && anchor.Before(staleBefore) {
			out = append(out, t)
		}
	}
	return out, nil
}

// parseRetryIntervals 解析 retry_intervals JSON 数组（秒）。
func parseRetryIntervals(s string) []int {
	if s == "" {
		return nil
	}
	var out []int
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}
