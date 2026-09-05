package inventory

// 锁卡生命周期（P1 最高优先——防超卖双路径）：
//
// MySQL/PG：事务内 SELECT ... WHERE status='available' ORDER BY id LIMIT n FOR UPDATE SKIP LOCKED
// → 全部置 reserved + order_id + locked_at；数量不足整批回滚
// SQLite： 单写者事务（BEGIN IMMEDIATE 语义由 database/sql 驱动自动处理）
// → UPDATE cards SET status='reserved' WHERE id IN (...) AND status='available'
// 校验 affected rows == n（CAS 语义，）
//
// Release：reserved → available，**必须同时清 order_id**（1.x 踩坑：unlock 不清 order_id 导致旧订单发货错乱）
// MarkUsed：UPDATE WHERE status='reserved' 校验 affected rows（防并发重发，友商纪律）
// TTL 释放：周期任务扫 locked_at 超时（ order 超时取消时调用）

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
)

// 错误。
var (
	ErrInsufficient = fmt.Errorf("inventory.INSUFFICIENT_STOCK")
	ErrNotFound     = fmt.Errorf("inventory.CARD_NOT_FOUND")
	ErrNotReserved  = fmt.Errorf("inventory.CARD_NOT_RESERVED")
)

// ── Reserve ───────────────────────────────────────────────────

// Reserve 事务内锁卡（防超卖）。必须在 data.Tx 内调用以共享事务。
func (r *CardRepoImpl) Reserve(ctx context.Context, subsiteID uint64, items []port.ReserveItem) (*port.Reservation, error) {
	client := data.Client(ctx, r.data)
	supportsLock := r.data.Dialect.Capabilities().SupportsSkipLocked
	var locked []port.ReservedCard // 锁到的卡（ 供货交付消费）

	for _, item := range items {
		if item.Quantity <= 0 {
			continue
		}
		// 1) 查可用卡（靓号精确匹配 or 按序取）
		query := client.Card.Query().
			Where(
				card.ProductID(item.ProductID),
				card.StatusEQ(card.StatusAvailable),
				card.SubsiteID(subsiteID),
			).
			Order(ent.Asc(card.FieldID)).
			Limit(int(item.Quantity))

		if item.NumberHash != "" {
			// 靓号自选：精确匹配
			query = client.Card.Query().
				Where(
					card.ProductID(item.ProductID),
					card.StatusEQ(card.StatusAvailable),
					card.SubsiteID(subsiteID),
					card.NumberHash(item.NumberHash),
				).
				Limit(1)
		}

		// 行锁（MySQL/PG；SQLite 单写者天然串行）
		if supportsLock {
			query = query.ForUpdate(entsql.WithLockAction(sql.SkipLocked))
		}

		rows, err := query.All(ctx)
		if err != nil {
			return nil, fmt.Errorf("inventory: 查询可用卡失败: %w", err)
		}
		want := int(item.Quantity)
		if item.NumberHash != "" {
			want = 1
		}
		if len(rows) < want {
			return nil, ErrInsufficient // 数量不足整批回滚
		}
		for _, row := range rows {
			locked = append(locked, port.ReservedCard{CardID: row.ID, Locked: true})
		}

		// 2) 置 reserved + order_id + locked_at（CAS：affected rows 校验）
		ids := make([]uint64, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		affected, err := client.Card.Update().
			Where(
				card.IDIn(ids...),
				card.StatusEQ(card.StatusAvailable),
			).
			SetStatus(card.StatusReserved).
			SetLockedAt(time.Now().UTC()).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("inventory: 锁卡失败: %w", err)
		}
		if int(affected) != len(ids) {
			return nil, ErrInsufficient // CAS 失败：并发竞争，回滚
		}
	}

	// 返回预留（caller 传入 order_id 后需调 BindOrder）
	return &port.Reservation{
		ReservationID: fmt.Sprintf("batch-%d", time.Now().UnixNano()),
		Cards:         locked,
		ExpiresAt:     time.Now().Add(30 * time.Minute).UTC(),
	}, nil
}

// BindOrder 锁卡后绑定订单（Reserve 成功 → caller 持有 reservation → 调用绑定）。
func (r *CardRepoImpl) BindOrder(ctx context.Context, subsiteID, productID, orderID uint64, quantity int32) error {
	client := data.Client(ctx, r.data)
	_, err := client.Card.Update().
		Where(
			card.ProductID(productID),
			card.SubsiteID(subsiteID),
			card.StatusEQ(card.StatusReserved),
			card.OrderIDIsNil(),
		).
		SetOrderID(orderID).
		Save(ctx)
	return err
}

// ── Release ───────────────────────────────────────────────────

// Release 释放预留（订单取消/超时；**必须清 order_id**——1.x 踩坑）。
func (r *CardRepoImpl) Release(ctx context.Context, orderID uint64) error {
	client := data.Client(ctx, r.data)
	_, err := client.Card.Update().
		Where(
			card.OrderID(orderID),
			card.StatusEQ(card.StatusReserved),
		).
		SetStatus(card.StatusAvailable).
		SetOrderID(0).
		ClearLockedAt().
		Save(ctx)
	return err
}

// ReleaseExpired TTL 释放（周期任务：locked_at 超时的 reserved → available）。
func (r *CardRepoImpl) ReleaseExpired(ctx context.Context, ttl time.Duration) (int, error) {
	client := data.Client(ctx, r.data)
	deadline := time.Now().Add(-ttl).UTC()
	n, err := client.Card.Update().
		Where(
			card.StatusEQ(card.StatusReserved),
			card.LockedAtLT(deadline),
		).
		SetStatus(card.StatusAvailable).
		SetOrderID(0).
		ClearLockedAt().
		Save(ctx)
	return int(n), err
}

// ── MarkUsed ──────────────────────────────────────────────────

// MarkUsed 售出标记（校验 affected rows 防并发重发）。
func (r *CardRepoImpl) MarkUsed(ctx context.Context, cardIDs []uint64, orderID uint64) error {
	client := data.Client(ctx, r.data)
	// orderID>0：本地订单严格校验绑定（防并发重发）；
	// orderID=0：供货交付（Reserve 未绑本地订单，order_id 为 NULL）
	upd := client.Card.Update().
		Where(
			card.IDIn(cardIDs...),
			card.StatusEQ(card.StatusReserved),
		)
	if orderID > 0 {
		upd = upd.Where(card.OrderID(orderID))
	}
	affected, err := upd.
		SetStatus(card.StatusUsed).
		SetUsedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("inventory: 标记售出失败: %w", err)
	}
	if int(affected) != len(cardIDs) {
		return ErrNotReserved // 部分卡不在 reserved 状态（并发冲突）
	}
	return nil
}

// ReleaseCards 释放指定锁卡（供货交付失败回滚；reserved → available）。
func (r *CardRepoImpl) ReleaseCards(ctx context.Context, cardIDs []uint64) error {
	_, err := data.Client(ctx, r.data).Card.Update().
		Where(
			card.IDIn(cardIDs...),
			card.StatusEQ(card.StatusReserved),
		).
		SetStatus(card.StatusAvailable).
		ClearOrderID().
		ClearLockedAt().
		Save(ctx)
	return err
}

// MarkUsedAndDelete 即删模式（delivery_mode=delete：发后物理删除卡密行）。
func (r *CardRepoImpl) MarkUsedAndDelete(ctx context.Context, cardIDs []uint64, orderID uint64) error {
	if err := r.MarkUsed(ctx, cardIDs, orderID); err != nil {
		return err
	}
	client := data.Client(ctx, r.data)
	_, err := client.Card.Delete().Where(card.IDIn(cardIDs...)).Exec(ctx)
	return err
}

// ── Stock ─────────────────────────────────────────────────────

// Stock 可用库存数（-1=无限，链接类）。
func (r *CardRepoImpl) Stock(ctx context.Context, productID, skuID uint64) (int64, error) {
	client := data.Client(ctx, r.data)
	q := client.Card.Query().Where(
		card.ProductID(productID),
		card.StatusEQ(card.StatusAvailable),
	)
	if skuID > 0 {
		q = q.Where(card.SkuID(skuID))
	}
	n, err := q.Count(ctx)
	return int64(n), err
}

// ListByOrder 按订单查卡（交付/取货用）。
func (r *CardRepoImpl) ListByOrder(ctx context.Context, orderID uint64) ([]*ent.Card, error) {
	return data.Client(ctx, r.data).Card.Query().
		Where(card.OrderID(orderID)).
		Order(ent.Asc(card.FieldID)).
		All(ctx)
}

// ── 编译期接口断言 ────────────────────────────────────────────
var _ port.Inventory = (*CardRepoImpl)(nil)
var _ = db.SQLite // 保持 db 引用

// Contents 交付卡密批量读取解密（ 供货交付出口）。
// 明文仅内存态返回，调用方负责 TLS 传输与零日志。
func (r *CardRepoImpl) Contents(ctx context.Context, cardIDs []uint64, productID, subsiteID uint64) ([]string, error) {
	rows, err := data.Client(ctx, r.data).Card.Query().
		Where(card.IDIn(cardIDs...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, c := range rows {
		plain, err := r.Cipher.Open(c.Content, c.ProductID, c.SubsiteID)
		if err != nil {
			return nil, fmt.Errorf("inventory: 卡密 %d 解密失败: %w", c.ID, err)
		}
		out = append(out, plain)
	}
	return out, nil
}
