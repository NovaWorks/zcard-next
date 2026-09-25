package supply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	snap "github.com/NovaWorks/zcard-next/server/internal/data/ent/supplycatalogsnapshot"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/google/uuid"
)

const catalogJobTimeout = 5 * time.Minute
const catalogSnapshotLifetime = 2 * time.Hour

var catalogWorkers = make(chan struct{}, 4)

// Only locally authored validation messages may bypass upstream error redaction.
type catalogLoadError string

func (e catalogLoadError) Error() string { return string(e) }

type catalogPayload struct {
	Categories []*adminv1.PreviewCategory `json:"categories"`
	Products   map[string]adapter.Product `json:"products"`
}

func catalogIdentity(c *ent.SupplyConnection) string { return fmt.Sprintf("%x", previewIdentity(c)) }

func (s *AdminSupplyService) previewSnapshot(ctx context.Context, req *adminv1.PreviewProductsRequest) (*adminv1.PreviewProductsReply, error) {
	conn, err := s.repo.GetConnection(ctx, req.ConnectionId)
	if err != nil {
		return nil, err
	}
	var row *ent.SupplyCatalogSnapshot
	if req.SnapshotId != "" {
		row, err = s.snapshotRow(ctx, conn, req.SnapshotId)
	} else {
		row, err = s.ensureCatalogSnapshot(ctx, conn.ID, req.Refresh)
	}
	if err != nil {
		return nil, err
	}
	if row.Status == "pending" || row.Status == "loading" {
		s.wakeCatalog(row.ID)
	}
	reply := &adminv1.PreviewProductsReply{SnapshotId: row.Token, Status: row.Status, Message: row.Message, LoadedCount: int32(row.LoadedCount), ExpiresAt: row.ExpiresAt}
	if row.Status != "ready" {
		return reply, nil
	}
	entry, err := decodeCatalog(row, conn)
	if err != nil {
		return nil, err
	}
	current, a, err := s.adapterForConnection(ctx, conn.ID)
	if err != nil {
		return nil, err
	}
	result, err := s.pricePreview(ctx, current, a, entry, req.QuoteCode)
	if err != nil {
		return nil, err
	}
	result.SnapshotId, result.Status, result.ExpiresAt = row.Token, "ready", row.ExpiresAt
	result.LoadedCount = int32(row.LoadedCount)
	return result, nil
}
func (s *AdminSupplyService) snapshotRow(ctx context.Context, conn *ent.SupplyConnection, token string) (*ent.SupplyCatalogSnapshot, error) {
	row, err := s.repo.entClient(ctx).SupplyCatalogSnapshot.Query().Where(snap.Token(token), snap.ConnectionID(conn.ID), snap.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, fmt.Errorf("商品目录已失效，请重新加载；已选内容会保留")
	}
	if err != nil {
		return nil, err
	}
	if row.Identity != catalogIdentity(conn) {
		return nil, fmt.Errorf("货源账号已变化，请重新加载商品目录")
	}
	if row.ExpiresAt <= time.Now().Unix() {
		return nil, fmt.Errorf("商品目录已过期，请重新加载；已选内容会保留")
	}
	return row, nil
}
func decodeCatalog(row *ent.SupplyCatalogSnapshot, conn *ent.SupplyConnection) (*previewEntry, error) {
	if row.Status != "ready" {
		return nil, fmt.Errorf("商品目录尚未加载完成，请稍后再试")
	}
	var payload catalogPayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		return nil, fmt.Errorf("商品目录快照不可用，请重新加载")
	}
	return &previewEntry{at: row.CreatedAt, identity: previewIdentity(conn), categories: payload.Categories, byCode: payload.Products}, nil
}
func (s *AdminSupplyService) readCatalogSnapshot(ctx context.Context, conn *ent.SupplyConnection, token string) (*previewEntry, *ent.SupplyCatalogSnapshot, error) {
	row, err := s.snapshotRow(ctx, conn, token)
	if err != nil {
		return nil, nil, err
	}
	entry, err := decodeCatalog(row, conn)
	return entry, row, err
}
func (s *AdminSupplyService) ensureCatalogSnapshot(ctx context.Context, connectionID uint64, refresh bool) (out *ent.SupplyCatalogSnapshot, err error) {
	err = data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := s.repo.entClient(ctx)
		// Serialize starts across processes, using the same portable write lock as import submission.
		if err := c.SupplyConnection.UpdateOneID(connectionID).AddRetryMax(0).Exec(ctx); err != nil {
			return err
		}
		conn, err := s.repo.GetConnection(ctx, connectionID)
		if err != nil {
			return err
		}
		if string(conn.Status) != "active" {
			return fmt.Errorf("货源已停用，请启用后再加载目录")
		}
		tenant := tenancy.FromContext(ctx).SubsiteID
		q := c.SupplyCatalogSnapshot.Query().Where(snap.ConnectionID(connectionID), snap.SubsiteID(tenant), snap.Identity(catalogIdentity(conn)), snap.ExpiresAtGT(time.Now().Unix()))
		row, e := q.Order(ent.Desc(snap.FieldID)).First(ctx)
		if e != nil && !ent.IsNotFound(e) {
			return e
		}
		// Explicit refresh retries failures; opening a failed job never creates an automatic retry loop.
		if e == nil && ((row.Status == "pending" || row.Status == "loading") || (!refresh && (row.Status == "failed" || time.Since(row.CreatedAt) < 15*time.Minute))) {
			out = row
			return nil
		}
		out, err = c.SupplyCatalogSnapshot.Create().SetConnectionID(connectionID).SetSubsiteID(tenant).SetToken(uuid.NewString()).SetIdentity(catalogIdentity(conn)).SetExpiresAt(time.Now().Add(catalogSnapshotLifetime).Unix()).SetMessage("正在等待加载上游目录，可以关闭窗口后再回来查看").Save(ctx)
		return err
	})
	return
}
func (s *AdminSupplyService) wakeCatalog(id uint64) {
	select {
	case catalogWorkers <- struct{}{}:
	default:
		return
	}
	go func() { defer func() { <-catalogWorkers }(); s.runCatalogSnapshot(id) }()
}

// ResumeCatalogSnapshots also works without Redis and recovers crashed readers after their lease expires.
func (s *AdminSupplyService) ResumeCatalogSnapshots(ctx context.Context) {
	c := s.repo.entClient(ctx)
	_, err := c.SupplyCatalogSnapshot.Delete().Where(snap.ExpiresAtLTE(time.Now().Unix())).Exec(ctx)
	if err != nil {
		slog.WarnContext(ctx, "supply.catalog.cleanup_failed", "error", err)
		return
	}
	ids, err := c.SupplyCatalogSnapshot.Query().Where(snap.StatusIn("pending", "loading"), snap.LeaseUntilLTE(time.Now().Unix())).Order(ent.Asc(snap.FieldID)).Limit(10).IDs(ctx)
	if err != nil {
		slog.WarnContext(ctx, "supply.catalog.scan_failed", "error", err)
		return
	}
	for _, id := range ids {
		s.wakeCatalog(id)
	}
}
func (s *AdminSupplyService) runCatalogSnapshot(id uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), catalogJobTimeout)
	defer cancel()
	c := s.repo.entClient(ctx)
	token := uuid.NewString()
	n, err := c.SupplyCatalogSnapshot.Update().Where(snap.ID(id), snap.StatusIn("pending", "loading"), snap.LeaseUntilLTE(time.Now().Unix()), snap.ExpiresAtGT(time.Now().Unix())).SetStatus("loading").SetLeaseToken(token).SetLeaseUntil(time.Now().Add(catalogJobTimeout + 30*time.Second).Unix()).AddAttempts(1).SetMessage("正在请求上游目录；单次最多等待 120 秒，临时故障最多重试一次").Save(ctx)
	if err != nil || n != 1 {
		return
	}
	row, err := c.SupplyCatalogSnapshot.Get(ctx, id)
	if err != nil {
		return
	}
	ctx = tenancy.WithContext(ctx, tenancy.Context{SubsiteID: row.SubsiteID, IsMain: row.SubsiteID == 0})
	var entry *previewEntry
	conn, err := s.repo.GetConnection(ctx, row.ConnectionID)
	if err == nil && (catalogIdentity(conn) != row.Identity || string(conn.Status) != "active") {
		err = catalogLoadError("货源已变化或停用，请检查货源配置后重新加载目录")
	}
	if row.Attempts > 3 {
		err = catalogLoadError("目录加载多次中断，请重新加载")
	}
	if err == nil {
		entry, err = s.fetchPreview(adapter.WithCatalogRead(ctx), row.ConnectionID, func(count int) {
			_ = c.SupplyCatalogSnapshot.Update().Where(snap.ID(id), snap.LeaseToken(token), snap.Status("loading")).SetLoadedCount(count).SetMessage(fmt.Sprintf("已读取 %d 件商品，正在整理目录…", count)).Exec(ctx)
		})
	}
	if err == nil {
		current, e := s.repo.GetConnection(ctx, row.ConnectionID)
		if e != nil {
			err = e
		} else if catalogIdentity(current) != row.Identity || string(current.Status) != "active" {
			err = catalogLoadError("货源已变化或停用，请检查货源配置后重新加载目录")
		}
	}
	var raw []byte
	if err == nil {
		raw, err = json.Marshal(catalogPayload{Categories: entry.categories, Products: entry.byCode})
		if len(raw) > 32*1024*1024 {
			err = catalogLoadError("商品目录超过存储上限，请缩小货源目录范围")
		}
	}
	// A timeout cannot prevent persisting the failed state.
	finish, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	update := c.SupplyCatalogSnapshot.Update().Where(snap.ID(id), snap.LeaseToken(token), snap.Status("loading")).SetLeaseUntil(0)
	if err != nil {
		failure := adapter.ClassifyImportError(err)
		message := failure.Summary + "，请稍后重试目录加载；不会导入或修改商品"
		if failure.Code == "TIMEOUT" {
			message = "上游商品目录长时间未响应（单次最多 120 秒，任务总时限 5 分钟），请稍后重试或检查服务器到上游的网络"
		}
		if failure.Code == "RATE_LIMITED" {
			message = "上游限流或网关拦截，已停止本次目录加载，请稍后重试"
		}
		var validation catalogLoadError
		if errors.As(err, &validation) {
			message = validation.Error()
		}
		slog.WarnContext(finish, "supply.catalog.failed", "snapshot", id, "connection", row.ConnectionID, "code", failure.Code)
		err = update.SetStatus("failed").SetMessage(message).Exec(finish)
	} else {
		err = update.SetStatus("ready").SetMessage("").SetPayload(raw).SetLoadedCount(len(entry.byCode)).Exec(finish)
	}
	if err != nil {
		slog.WarnContext(finish, "supply.catalog.finish_failed", "snapshot", id, "error", err)
	}
}
