package supply

// 管理面 API（P2-01）：连接 CRUD + Ping、映射规则、同步任务、健康列表。
// 凭据纪律：credentials 仅创建/重配时明文接收；任何响应零回显（credentials_set 布尔）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplysynctask"
	"github.com/NovaWorks/zcard-next/server/internal/platform/httpx"

	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminSupplyService 管理面货源服务。
type AdminSupplyService struct {
	adminv1.UnimplementedAdminSupplyServiceServer
	repo *SupplyRepoImpl
	sync *SyncService
}

// NewAdminSupplyService 构造。
func NewAdminSupplyService(repo *SupplyRepoImpl, sync *SyncService) *AdminSupplyService {
	return &AdminSupplyService{repo: repo, sync: sync}
}

// ── 连接 ──────────────────────────────────────────────────

// CreateConnection 创建连接：driver 校验 + base_url SSRF 校验 + 凭据加密。
func (s *AdminSupplyService) CreateConnection(ctx context.Context, req *adminv1.CreateConnectionRequest) (*adminv1.SupplyConnection, error) {
	if req.GetDriver() == "" {
		return nil, errors.New("supply.DRIVER_REQUIRED: 请选择驱动")
	}
	if req.GetBaseUrl() == "" {
		return nil, errors.New("supply.BASE_URL_REQUIRED")
	}
	if err := httpx.ValidateURL(req.GetBaseUrl()); err != nil {
		return nil, fmt.Errorf("supply.SSRF_REJECTED: %w", err)
	}
	if req.GetCredentials() == "" {
		return nil, errors.New("supply.CREDENTIALS_REQUIRED: 凭据必填（仅创建时可设置）")
	}
	enc, err := s.repo.SealCredentials(req.GetDriver(), req.GetBaseUrl(), req.GetCredentials())
	if err != nil {
		return nil, err
	}
	conn, err := s.repo.CreateConnection(ctx, &ent.SupplyConnection{
		Name:               req.GetName(),
		Driver:             req.GetDriver(),
		BaseURL:            req.GetBaseUrl(),
		Credentials:        enc,
		Status:             supplyconnection.StatusActive,
		CallbackURL:        req.GetCallbackUrl(),
		RetryMax:           req.GetRetryMax(),
		RetryIntervals:     req.GetRetryIntervals(),
		ExchangeRate:       req.GetExchangeRate(),
		PriceMarkupPercent: req.GetPriceMarkupPercent(),
		PriceRoundingMode:  supplyconnection.PriceRoundingMode(orDefault(req.GetPriceRoundingMode(), "none")),
		AutoSyncPrice:      req.GetAutoSyncPrice(),
		StockMode:          supplyconnection.StockMode(orDefault(req.GetStockMode(), "real")),
		Settings:           mustJSONMap(req.GetSettings()),
	})
	if err != nil {
		return nil, err
	}
	return toProtoConnection(conn), nil
}

// UpdateConnection 更新连接（credentials 留空 = 不更新凭据）。
func (s *AdminSupplyService) UpdateConnection(ctx context.Context, req *adminv1.UpdateConnectionRequest) (*adminv1.SupplyConnection, error) {
	conn, err := s.repo.GetConnection(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if req.GetBaseUrl() != "" && req.GetBaseUrl() != conn.BaseURL {
		if err := httpx.ValidateURL(req.GetBaseUrl()); err != nil {
			return nil, fmt.Errorf("supply.SSRF_REJECTED: %w", err)
		}
	}
	upd := &ent.SupplyConnection{
		Name:               req.GetName(),
		BaseURL:            req.GetBaseUrl(),
		CallbackURL:        req.GetCallbackUrl(),
		RetryMax:           req.GetRetryMax(),
		RetryIntervals:     req.GetRetryIntervals(),
		ExchangeRate:       req.GetExchangeRate(),
		PriceMarkupPercent: req.GetPriceMarkupPercent(),
		PriceRoundingMode:  supplyconnection.PriceRoundingMode(req.GetPriceRoundingMode()),
		AutoSyncPrice:      req.GetAutoSyncPrice(),
		StockMode:          supplyconnection.StockMode(req.GetStockMode()),
		Status:             supplyconnection.Status(req.GetStatus()),
	}
	updated, err := s.repo.UpdateConnection(ctx, req.GetId(), upd)
	if err != nil {
		return nil, err
	}
	// 凭据单独更新（换 base_url 时凭据需重配：AAD 绑定 base_url）
	if req.GetCredentials() != "" {
		newBase := req.GetBaseUrl()
		if newBase == "" {
			newBase = conn.BaseURL
		}
		if err := s.repo.UpdateCredentials(ctx, req.GetId(), conn.Driver, newBase, req.GetCredentials()); err != nil {
			return nil, err
		}
	}
	return toProtoConnection(updated), nil
}

// DeleteConnection 删除连接（存在映射时拒绝）。
func (s *AdminSupplyService) DeleteConnection(ctx context.Context, req *adminv1.DeleteConnectionRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteConnection(ctx, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// ListConnections 连接列表（凭据零回显）。
func (s *AdminSupplyService) ListConnections(ctx context.Context, req *adminv1.ListConnectionsRequest) (*adminv1.ListConnectionsReply, error) {
	page, pageSize := pageParams(req.GetPage(), req.GetPageSize())
	conns, total, err := s.repo.ListConnections(ctx, page, pageSize)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListConnectionsReply{Total: int64(total), Page: int32(page), PageSize: int32(pageSize)}
	for _, c := range conns {
		reply.Connections = append(reply.Connections, toProtoConnection(c))
	}
	return reply, nil
}

// PingConnection 探活。
func (s *AdminSupplyService) PingConnection(ctx context.Context, req *adminv1.PingConnectionRequest) (*adminv1.PingConnectionReply, error) {
	res, err := s.sync.PingConnection(ctx, req.GetId())
	if err != nil {
		return &adminv1.PingConnectionReply{Ok: false, Error: err.Error()}, nil
	}
	reply := &adminv1.PingConnectionReply{Ok: true}
	if res != nil {
		reply.SiteName = res.SiteName
		reply.Balance = res.Balance
		reply.Currency = res.Currency
	}
	return reply, nil
}

// ── 映射 ──────────────────────────────────────────────────

// ListMappings 映射列表。
func (s *AdminSupplyService) ListMappings(ctx context.Context, req *adminv1.ListMappingsRequest) (*adminv1.ListMappingsReply, error) {
	page, pageSize := pageParams(req.GetPage(), req.GetPageSize())
	ms, total, err := s.repo.ListMappings(ctx, req.GetConnectionId(), page, pageSize)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListMappingsReply{Total: int64(total), Page: int32(page), PageSize: int32(pageSize)}
	for _, m := range ms {
		reply.Mappings = append(reply.Mappings, toProtoMapping(m))
	}
	return reply, nil
}

// UpsertMapping 创建/更新映射。
func (s *AdminSupplyService) UpsertMapping(ctx context.Context, req *adminv1.UpsertMappingRequest) (*adminv1.SupplyMapping, error) {
	m, err := s.repo.UpsertMapping(ctx, &ent.SupplyMapping{
		ConnectionID:     req.GetConnectionId(),
		UpstreamCategory: req.GetUpstreamCategory(),
		LocalCategoryID:  req.GetLocalCategoryId(),
		UpstreamProduct:  req.GetUpstreamProduct(),
		LocalProductID:   req.GetLocalProductId(),
		UpstreamSku:      req.GetUpstreamSku(),
		LocalSkuID:       req.GetLocalSkuId(),
		PricingOverride:  mustJSONMap(req.GetPricingOverride()),
	})
	if err != nil {
		return nil, err
	}
	return toProtoMapping(m), nil
}

// DeleteMapping 删除映射。
func (s *AdminSupplyService) DeleteMapping(ctx context.Context, req *adminv1.DeleteMappingRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteMapping(ctx, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// ── 同步任务 ──────────────────────────────────────────────

// CreateSyncTask 创建同步任务并入队（low 队列；降级模式进程内异步）。
func (s *AdminSupplyService) CreateSyncTask(ctx context.Context, req *adminv1.CreateSyncTaskRequest) (*adminv1.SupplySyncTask, error) {
	if _, err := s.repo.GetConnection(ctx, req.GetConnectionId()); err != nil {
		return nil, err
	}
	mode := req.GetMode()
	if mode == "" {
		mode = "full"
	}
	task, err := s.repo.CreateSyncTask(ctx, req.GetConnectionId(), mode, req.GetScope(), req.GetForceReprice())
	if err != nil {
		return nil, err
	}
	if err := s.sync.StartTask(ctx, task.ID); err != nil {
		_ = s.repo.FinishTask(ctx, task.ID, supplysynctask.StatusFailed, "ENQUEUE_FAILED", err.Error())
		return nil, err
	}
	return toProtoTask(task), nil
}

// ListSyncTasks 任务列表。
func (s *AdminSupplyService) ListSyncTasks(ctx context.Context, req *adminv1.ListSyncTasksRequest) (*adminv1.ListSyncTasksReply, error) {
	page, pageSize := pageParams(req.GetPage(), req.GetPageSize())
	ts, total, err := s.repo.ListSyncTasks(ctx, req.GetConnectionId(), page, pageSize)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListSyncTasksReply{Total: int64(total), Page: int32(page), PageSize: int32(pageSize)}
	for _, t := range ts {
		reply.Tasks = append(reply.Tasks, toProtoTask(t))
	}
	return reply, nil
}

// GetSyncTask 任务详情。
func (s *AdminSupplyService) GetSyncTask(ctx context.Context, req *adminv1.GetSyncTaskRequest) (*adminv1.SupplySyncTask, error) {
	t, err := s.repo.GetSyncTask(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoTask(t), nil
}

// CancelSyncTask 请求取消。
func (s *AdminSupplyService) CancelSyncTask(ctx context.Context, req *adminv1.CancelSyncTaskRequest) (*adminv1.SupplySyncTask, error) {
	t, err := s.repo.RequestCancel(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoTask(t), nil
}

// ── 健康 ──────────────────────────────────────────────────

// ListHealth 连接健康列表。
func (s *AdminSupplyService) ListHealth(ctx context.Context, _ *adminv1.ListHealthRequest) (*adminv1.ListHealthReply, error) {
	items, err := s.sync.ListHealth(ctx)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListHealthReply{}
	for _, it := range items {
		reply.Items = append(reply.Items, &adminv1.HealthItem{
			ConnectionId:    it.ConnectionID,
			Name:            it.Name,
			Driver:          it.Driver,
			Status:          it.Status,
			LastPingOk:      it.LastPingOK,
			LastPingAt:      it.LastPingAt,
			LastSyncedAt:    it.LastSyncedAt,
			LastError:       it.LastError,
			BalanceCache:    it.BalanceCache,
			PingSuccessRate: it.PingSuccessRate,
			AvgLatencyMs:    it.AvgLatencyMs,
		})
	}
	return reply, nil
}

// ── 转换 ──────────────────────────────────────────────────

func toProtoConnection(c *ent.SupplyConnection) *adminv1.SupplyConnection {
	p := &adminv1.SupplyConnection{
		Id:                 c.ID,
		Name:               c.Name,
		Driver:             c.Driver,
		BaseUrl:            c.BaseURL,
		CredentialsSet:     len(c.Credentials) > 0,
		Status:             string(c.Status),
		CallbackUrl:        c.CallbackURL,
		RetryMax:           c.RetryMax,
		RetryIntervals:     c.RetryIntervals,
		ExchangeRate:       c.ExchangeRate,
		PriceMarkupPercent: c.PriceMarkupPercent,
		PriceRoundingMode:  string(c.PriceRoundingMode),
		AutoSyncPrice:      c.AutoSyncPrice,
		StockMode:          string(c.StockMode),
		LastPingOk:         c.LastPingOk,
		BalanceCache:       c.BalanceCache,
		LastError:          c.LastError,
		CreatedAt:          c.CreatedAt.Unix(),
		UpdatedAt:          c.UpdatedAt.Unix(),
	}
	if c.Settings != nil {
		if b, err := json.Marshal(c.Settings); err == nil {
			p.Settings = string(b)
		}
	}
	if !c.LastPingAt.IsZero() {
		p.LastPingAt = c.LastPingAt.Unix()
	}
	if !c.LastSyncedAt.IsZero() {
		p.LastSyncedAt = c.LastSyncedAt.Unix()
	}
	return p
}

func toProtoMapping(m *ent.SupplyMapping) *adminv1.SupplyMapping {
	p := &adminv1.SupplyMapping{
		Id:               m.ID,
		ConnectionId:     m.ConnectionID,
		UpstreamCategory: m.UpstreamCategory,
		LocalCategoryId:  m.LocalCategoryID,
		UpstreamProduct:  m.UpstreamProduct,
		LocalProductId:   m.LocalProductID,
		UpstreamSku:      m.UpstreamSku,
		LocalSkuId:       m.LocalSkuID,
		UpStock:          m.UpStock,
		CreatedAt:        m.CreatedAt.Unix(),
		UpdatedAt:        m.UpdatedAt.Unix(),
	}
	if m.PricingOverride != nil {
		if b, err := json.Marshal(m.PricingOverride); err == nil {
			p.PricingOverride = string(b)
		}
	}
	return p
}

func toProtoTask(t *ent.SupplySyncTask) *adminv1.SupplySyncTask {
	p := &adminv1.SupplySyncTask{
		Id:            t.ID,
		ConnectionId:  t.ConnectionID,
		Mode:          t.Mode,
		Scope:         t.Scope,
		ForceReprice:  t.ForceReprice,
		Status:        string(t.Status),
		Total:         int64(t.TotalCount),
		Processed:     int64(t.ProcessedCount),
		Created:       int64(t.CreatedCount),
		Updated:       int64(t.UpdatedCount),
		PriceUpdated:  int64(t.PriceUpdatedCount),
		ManualSkipped: int64(t.ManualSkippedCount),
		Hidden:        int64(t.HiddenCount),
		Deleted:       int64(t.DeletedCount),
		ErrorCode:     t.ErrorCode,
		ErrorContext:  t.ErrorContext,
		CurrentStage:  t.CurrentStage,
		Page:          int32(t.CurrentPage),
	}
	if !t.StartedAt.IsZero() {
		p.StartedAt = t.StartedAt.Unix()
	}
	if !t.HeartbeatAt.IsZero() {
		p.HeartbeatAt = t.HeartbeatAt.Unix()
	}
	if !t.CancelRequestedAt.IsZero() {
		p.CancelRequestedAt = t.CancelRequestedAt.Unix()
	}
	if !t.FinishedAt.IsZero() {
		p.FinishedAt = t.FinishedAt.Unix()
	}
	return p
}

// pageParams 分页参数归一化（扁平 int32；page 从 1 起）。
func pageParams(page, pageSize int32) (int, int) {
	p := int(page)
	if p < 1 {
		p = 1
	}
	ps := int(pageSize)
	if ps < 1 {
		ps = 20
	}
	if ps > 100 {
		ps = 100
	}
	return p, ps
}

// mustJSONMap 解析 JSON 字符串为 map（空串 → nil；非法 → nil 不阻断）。
func mustJSONMap(s string) map[string]any {
	if s == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}

// orDefault 空串回落默认值（枚举字段 proto3 零值语义）。
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
