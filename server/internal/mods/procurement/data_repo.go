package procurement

// 采购单仓储（P2-02 T1/T2）：两表 CRUD + 状态机 CAS（非法迁移拒绝）。
//
// 状态机（§5.7.2）：
//   pending → submitted（受理，记 upstream_order_id）
//   pending → fulfilled（同步拿货成功）
//   pending → rejected（永久错误：无映射/下架/余额不足）
//   submitted → polling → fulfilled / rejected
//   rejected → refunding → refunded（auto_refund 失败策略）| manual（人工终态）
//   polling → manual（24h 卡死转人工）

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
)

// 哨兵错误。
var (
	ErrNotFound          = errors.New("procurement: 采购单不存在")
	ErrTransitionDenied  = errors.New("procurement: 非法状态迁移")
	ErrConcurrentUpdate  = errors.New("procurement: 并发更新冲突")
	ErrDuplicatePurchase = errors.New("procurement: 该订单项已存在采购单")
)

// ProcureRepo 采购仓储。
type ProcureRepo struct {
	data *data.Data
}

// NewProcureRepo 构造。
func NewProcureRepo(d *data.Data) *ProcureRepo { return &ProcureRepo{data: d} }

// 合法迁移表（并发三通道汇聚同一终态只生效一次：CAS + 迁移表双保险）。
var allowedTransitions = map[string]map[string]bool{
	"pending":   {"submitted": true, "fulfilled": true, "rejected": true, "polling": true},
	"submitted": {"polling": true, "fulfilled": true, "rejected": true, "refunding": true, "manual": true},
	"polling":   {"fulfilled": true, "rejected": true, "refunding": true, "manual": true},
	"rejected":  {"refunding": true, "manual": true, "refunded": true},
	"refunding": {"refunded": true, "manual": true},
}

// transition 状态机 CAS（乐观锁 + 迁移表；并发到达同一终态只生效一次）。
func (r *ProcureRepo) transition(ctx context.Context, id uint64, from, to string, mutate func(upd *ent.ProcurementOrderUpdateOne) *ent.ProcurementOrderUpdateOne) error {
	cur, err := data.Client(ctx, r.data).ProcurementOrder.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return err
	}
	if string(cur.Status) != from {
		return ErrTransitionDenied
	}
	if !allowedTransitions[from][to] {
		return fmt.Errorf("%w: %s → %s", ErrTransitionDenied, from, to)
	}
	upd := data.Client(ctx, r.data).ProcurementOrder.UpdateOneID(id).
		Where(procurementorder.StatusEQ(procurementorder.Status(from))).
		SetStatus(procurementorder.Status(to))
	if mutate != nil {
		upd = mutate(upd)
	}
	saved, err := upd.Save(ctx)
	if err != nil {
		return err
	}
	if saved == nil {
		return ErrConcurrentUpdate // 并发已迁移
	}
	return nil
}

// CreatePending 创建采购单（幂等：order_item_id 唯一 → 重复返回 ErrDuplicatePurchase）。
func (r *ProcureRepo) CreatePending(ctx context.Context, orderItemID, connectionID uint64, productCode string, quantity int32, failStrategy string, traceID string) (*ent.ProcurementOrder, error) {
	dedupe := fmt.Sprintf("order_item:%d", orderItemID)
	p, err := data.Client(ctx, r.data).ProcurementOrder.Create().
		SetOrderItemID(orderItemID).
		SetConnectionID(connectionID).
		SetStatus(procurementorder.StatusPending).
		SetFailStrategy(procurementorder.FailStrategy(failStrategy)).
		SetDedupeKey(dedupe).
		SetTraceID(traceID).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, ErrDuplicatePurchase
		}
		return nil, err
	}
	// 采购项（sku 快照）
	_, err = data.Client(ctx, r.data).ProcurementItem.Create().
		SetProcurementID(p.ID).
		SetUpstreamSku(productCode).
		SetQuantity(quantity).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Get 采购单详情。
func (r *ProcureRepo) Get(ctx context.Context, id uint64) (*ent.ProcurementOrder, error) {
	p, err := data.Client(ctx, r.data).ProcurementOrder.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// GetByOrderItem 按订单项查采购单（幂等判据）。
func (r *ProcureRepo) GetByOrderItem(ctx context.Context, orderItemID uint64) (*ent.ProcurementOrder, error) {
	p, err := data.Client(ctx, r.data).ProcurementOrder.Query().
		Where(procurementorder.OrderItemID(orderItemID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// List 采购单分页（按状态过滤）。
func (r *ProcureRepo) List(ctx context.Context, status string, page, pageSize int) ([]*ent.ProcurementOrder, int, error) {
	q := data.Client(ctx, r.data).ProcurementOrder.Query().Order(ent.Desc(procurementorder.FieldID))
	if status != "" {
		q = q.Where(procurementorder.StatusEQ(procurementorder.Status(status)))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * pageSize).Limit(pageSize).All(ctx)
	return rows, total, err
}

// ListPollable polling/submitted 单（巡检拉取；INDEX(status, last_poll_at) 命中）。
func (r *ProcureRepo) ListPollable(ctx context.Context, limit int) ([]*ent.ProcurementOrder, error) {
	return data.Client(ctx, r.data).ProcurementOrder.Query().
		Where(procurementorder.StatusIn(procurementorder.StatusSubmitted, procurementorder.StatusPolling)).
		Order(ent.Asc(procurementorder.FieldLastPollAt)).
		Limit(limit).
		All(ctx)
}

// MarkSubmitted 受理（pending → submitted，记 upstream_order_id + 退避）。
func (r *ProcureRepo) MarkSubmitted(ctx context.Context, id uint64, upstreamOrderID string, nextRetryAt time.Time) error {
	return r.transition(ctx, id, "pending", "submitted", func(upd *ent.ProcurementOrderUpdateOne) *ent.ProcurementOrderUpdateOne {
		return upd.SetUpstreamOrderID(upstreamOrderID).SetNextRetryAt(nextRetryAt)
	})
}

// MarkPolling submitted/pending → polling（轮询前）。
func (r *ProcureRepo) MarkPolling(ctx context.Context, id uint64) error {
	return r.transition(ctx, id, "submitted", "polling", func(upd *ent.ProcurementOrderUpdateOne) *ent.ProcurementOrderUpdateOne {
		return upd.SetLastPollAt(time.Now().UTC())
	})
}

// MarkFulfilled 采购完成（submitted/polling/pending → fulfilled）。
func (r *ProcureRepo) MarkFulfilled(ctx context.Context, id uint64) error {
	// 允许 pending/submitted/polling 三个来源（同步拿货/轮询/巡检汇聚）
	for _, from := range []string{"pending", "submitted", "polling"} {
		err := r.transition(ctx, id, from, "fulfilled", nil)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrTransitionDenied) && !errors.Is(err, ErrConcurrentUpdate) {
			return err
		}
	}
	return ErrTransitionDenied
}

// MarkRejected 永久错误（→ 失败策略分流）。
func (r *ProcureRepo) MarkRejected(ctx context.Context, id uint64, reason string) error {
	for _, from := range []string{"pending", "submitted", "polling"} {
		err := r.transition(ctx, id, from, "rejected", func(upd *ent.ProcurementOrderUpdateOne) *ent.ProcurementOrderUpdateOne {
			return upd.SetLastError(reason)
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrTransitionDenied) && !errors.Is(err, ErrConcurrentUpdate) {
			return err
		}
	}
	return ErrTransitionDenied
}

// MarkRefunding rejected → refunding（auto_refund 分流开始）。
func (r *ProcureRepo) MarkRefunding(ctx context.Context, id uint64) error {
	return r.transition(ctx, id, "rejected", "refunding", nil)
}

// MarkRefunded refunding → refunded（上游退款成功）。
func (r *ProcureRepo) MarkRefunded(ctx context.Context, id uint64, upstreamRefundID string) error {
	return r.transition(ctx, id, "refunding", "refunded", func(upd *ent.ProcurementOrderUpdateOne) *ent.ProcurementOrderUpdateOne {
		return upd.SetUpstreamRefundID(upstreamRefundID)
	})
}

// MarkManual → manual（人工终态：失败策略分流 / 24h 卡死）。
func (r *ProcureRepo) MarkManual(ctx context.Context, id uint64, reason string) error {
	for _, from := range []string{"rejected", "submitted", "polling", "refunding"} {
		err := r.transition(ctx, id, from, "manual", func(upd *ent.ProcurementOrderUpdateOne) *ent.ProcurementOrderUpdateOne {
			return upd.SetLastError(reason)
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrTransitionDenied) && !errors.Is(err, ErrConcurrentUpdate) {
			return err
		}
	}
	return ErrTransitionDenied
}

// BumpRetry 退避推进（retry_count+1，next_retry_at 后移）。
func (r *ProcureRepo) BumpRetry(ctx context.Context, id uint64, nextRetryAt time.Time, lastErr string) error {
	p, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	_, err = data.Client(ctx, r.data).ProcurementOrder.UpdateOneID(id).
		SetRetryCount(p.RetryCount + 1).
		SetNextRetryAt(nextRetryAt).
		SetLastError(lastErr).
		Save(ctx)
	return err
}

// AttachReceivedContent 落采购项密文（到手即加密 T4：received_content 全密文）。
func (r *ProcureRepo) AttachReceivedContent(ctx context.Context, procurementID uint64, contents [][]byte) error {
	item, err := data.Client(ctx, r.data).ProcurementItem.Query().
		Where(procurementitem.ProcurementID(procurementID)).
		Only(ctx)
	if err != nil {
		return err
	}
	// 单 SKU 采购项：多卡密以行序列化存储（JSON 数组 of base64）
	rows := make([]string, 0, len(contents))
	for _, c := range contents {
		rows = append(rows, encodeB64(c))
	}
	_, err = data.Client(ctx, r.data).ProcurementItem.UpdateOneID(item.ID).
		SetReceivedContent(rows).
		Save(ctx)
	return err
}

// ReceivedContent 读采购项密文（[][]byte）。
func (r *ProcureRepo) ReceivedContent(ctx context.Context, procurementID uint64) ([][]byte, error) {
	item, err := data.Client(ctx, r.data).ProcurementItem.Query().
		Where(procurementitem.ProcurementID(procurementID)).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, 0, len(item.ReceivedContent))
	for _, s := range item.ReceivedContent {
		out = append(out, decodeB64(s))
	}
	return out, nil
}

// EnsureTraceID 采购单 trace_id（排障贯穿）。
func (r *ProcureRepo) EnsureTraceID(ctx context.Context, id uint64, traceID string) error {
	if traceID == "" {
		return nil
	}
	_, err := data.Client(ctx, r.data).ProcurementOrder.UpdateOneID(id).SetTraceID(traceID).Save(ctx)
	return err
}

// ItemByProcurement 采购项（sku 快照/成本）。
func (r *ProcureRepo) ItemByProcurement(ctx context.Context, procurementID uint64) (*ent.ProcurementItem, error) {
	item, err := data.Client(ctx, r.data).ProcurementItem.Query().
		Where(procurementitem.ProcurementID(procurementID)).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	return item, nil
}

// OrderItemInfo 订单项上下文（order_id / product_id / subsite_id——交付出口与加密 AAD 需要）。
type OrderItemInfo struct {
	OrderID   uint64
	ProductID uint64
	SubsiteID uint64
	Quantity  int32
}

// OrderItemInfo 按 order_item_id 查订单项上下文。
func (r *ProcureRepo) OrderItemInfo(ctx context.Context, orderItemID uint64) (*OrderItemInfo, error) {
	it, err := data.Client(ctx, r.data).OrderItem.Get(ctx, orderItemID)
	if err != nil {
		return nil, err
	}
	return &OrderItemInfo{OrderID: it.OrderID, ProductID: it.ProductID, SubsiteID: it.SubsiteID, Quantity: it.Quantity}, nil
}

// OrderIDOf 采购单 → 订单 ID（失败策略退款需要；经 order_item 反查）。
func (r *ProcureRepo) OrderIDOf(ctx context.Context, procurementID uint64) (uint64, error) {
	po, err := r.Get(ctx, procurementID)
	if err != nil {
		return 0, err
	}
	it, err := data.Client(ctx, r.data).OrderItem.Get(ctx, po.OrderItemID)
	if err != nil {
		return 0, err
	}
	return it.OrderID, nil
}
