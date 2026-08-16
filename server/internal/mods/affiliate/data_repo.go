package affiliate

// 佣金仓储（P3-03）：幂等入账（UNIQUE(order_id,tier)）、冻结确认、逆向扣回（负债态）。

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/affiliatecommission"
	entUser "github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/mods/affiliate/port"
)

// 哨兵错误。
var (
	ErrDuplicate = errors.New("affiliate: 佣金已存在（幂等 ACK）")
)

// CommissionRepo 佣金仓储。
type CommissionRepo struct {
	data *data.Data
}

// NewCommissionRepo 构造。
func NewCommissionRepo(d *data.Data) *CommissionRepo { return &CommissionRepo{data: d} }

// CommissionRow 入账行。
type CommissionRow struct {
	OrderID     uint64
	BuyerID     uint64
	ReferrerID  uint64
	Tier        int8 // 1|2|3
	Rate        int32
	BaseAmount  int64
	Amount      int64
	AvailableAt time.Time
}

// Insert 幂等入账（UNIQUE(order_id, tier) 冲突 → ErrDuplicate）。
func (r *CommissionRepo) Insert(ctx context.Context, row CommissionRow) error {
	_, err := data.Client(ctx, r.data).AffiliateCommission.Create().
		SetOrderID(row.OrderID).
		SetBuyerID(row.BuyerID).
		SetReferrerID(row.ReferrerID).
		SetTier(row.Tier).
		SetRate(float64(row.Rate)).
		SetBaseAmount(row.BaseAmount).
		SetAmount(row.Amount).
		SetStatus(affiliatecommission.StatusPendingConfirm).
		SetAvailableAt(row.AvailableAt).
		Save(ctx)
	if ent.IsConstraintError(err) {
		return ErrDuplicate
	}
	return err
}

// ListDueConfirm 到期待确认（available_at <= now 且 pending_confirm）。
func (r *CommissionRepo) ListDueConfirm(ctx context.Context, now time.Time, limit int) ([]*ent.AffiliateCommission, error) {
	return data.Client(ctx, r.data).AffiliateCommission.Query().
		Where(
			affiliatecommission.StatusEQ(affiliatecommission.StatusPendingConfirm),
			affiliatecommission.AvailableAtLTE(now),
		).
		Order(ent.Asc(affiliatecommission.FieldID)).
		Limit(limit).
		All(ctx)
}

// MarkAvailable 确认（pending_confirm → available）。
func (r *CommissionRepo) MarkAvailable(ctx context.Context, id uint64) error {
	_, err := data.Client(ctx, r.data).AffiliateCommission.UpdateOneID(id).
		SetStatus(affiliatecommission.StatusAvailable).
		Save(ctx)
	return err
}

// ListByOrder 订单全部佣金行（逆向扣回输入）。
func (r *CommissionRepo) ListByOrder(ctx context.Context, orderID uint64) ([]*ent.AffiliateCommission, error) {
	return data.Client(ctx, r.data).AffiliateCommission.Query().
		Where(affiliatecommission.OrderID(orderID)).
		All(ctx)
}

// MarkReversed 逆向（→ reversed）。
func (r *CommissionRepo) MarkReversed(ctx context.Context, id uint64) error {
	_, err := data.Client(ctx, r.data).AffiliateCommission.UpdateOneID(id).
		SetStatus(affiliatecommission.StatusReversed).
		Save(ctx)
	return err
}

// InsertDebt 负债行（负数佣金；tier 取负标记与原行区分 UNIQUE(order_id,tier)；
// cron 重试扣款直至余额充足——负债态抵扣后续佣金语义）。
func (r *CommissionRepo) InsertDebt(ctx context.Context, orderID, referrerID uint64, tier int8, amount int64) error {
	_, err := data.Client(ctx, r.data).AffiliateCommission.Create().
		SetOrderID(orderID).
		SetBuyerID(0). // 负债行无买家语义（逆向产生）
		SetReferrerID(referrerID).
		SetTier(-tier). // 负 tier = 负债行（与原佣金行同单不撞唯一键）
		SetRate(0).
		SetBaseAmount(0).
		SetAmount(-amount). // 负数 = 负债
		SetStatus(affiliatecommission.StatusPendingConfirm).
		SetAvailableAt(time.Now().UTC()).
		Save(ctx)
	if ent.IsConstraintError(err) {
		return nil // 同 (order,tier) 负债已登记（幂等）
	}
	return err
}

// ListByUser 用户佣金流水（正 + 负债）。
func (r *CommissionRepo) ListByUser(ctx context.Context, userID uint64, page, size int) ([]*ent.AffiliateCommission, int, error) {
	q := data.Client(ctx, r.data).AffiliateCommission.Query().
		Where(affiliatecommission.ReferrerID(userID)).
		Order(ent.Desc(affiliatecommission.FieldID))
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// StatsByUser 用户统计（port.CommissionReader 实现）。
func (r *CommissionRepo) StatsByUser(ctx context.Context, userID uint64) (*port.CommissionStats, error) {
	rows, err := data.Client(ctx, r.data).AffiliateCommission.Query().
		Where(affiliatecommission.ReferrerID(userID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := &port.CommissionStats{}
	for _, c := range rows {
		switch string(c.Status) {
		case "pending_confirm":
			if c.Amount >= 0 {
				out.PendingCents += c.Amount
			} else {
				out.DebtCents += -c.Amount // 负债行也以 pending 态重试扣款
			}
		case "available":
			out.AvailableCents += c.Amount
		case "withdrawn":
			out.WithdrawnCents += c.Amount
		case "reversed":
			_ = c
		}
		if c.Amount > 0 {
			out.TotalCents += c.Amount
		}
	}
	return out, nil
}

// ListAll 管理面列表（按状态筛选）。
func (r *CommissionRepo) ListAll(ctx context.Context, status string, page, size int) ([]*ent.AffiliateCommission, int, error) {
	q := data.Client(ctx, r.data).AffiliateCommission.Query().Order(ent.Desc(affiliatecommission.FieldID))
	if status != "" {
		q = q.Where(affiliatecommission.StatusEQ(affiliatecommission.Status(status)))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// ListCommissions port 方法（DTO 转换）。
func (r *CommissionRepo) ListCommissions(ctx context.Context, status string, page, size int) ([]port.CommissionRow, int64, error) {
	rows, total, err := r.ListAll(ctx, status, page, size)
	if err != nil {
		return nil, 0, err
	}
	out := make([]port.CommissionRow, 0, len(rows))
	for _, c := range rows {
		row := port.CommissionRow{
			ID: c.ID, OrderID: c.OrderID, BuyerID: c.BuyerID, ReferrerID: c.ReferrerID,
			Tier: int32(c.Tier), Rate: c.Rate, BaseAmount: c.BaseAmount, Amount: c.Amount,
			Status: string(c.Status), CreatedAt: c.CreatedAt.Unix(),
		}
		if !c.AvailableAt.IsZero() {
			row.AvailableAt = c.AvailableAt.Unix()
		}
		out = append(out, row)
	}
	return out, int64(total), nil
}

// refKey 幂等键（wallet 入账）。
func refKey(id uint64) string { return fmt.Sprintf("commission:%d", id) }

// debtRefKey 负债扣款幂等键。
func debtRefKey(id uint64) string { return fmt.Sprintf("commission:debt:%d", id) }

// TeamCounts 三级团队人数。
func (r *CommissionRepo) TeamCounts(ctx context.Context, userID uint64) (l1, l2, l3 int64, err error) {
	client := data.Client(ctx, r.data)
	c1, err := client.User.Query().Where(entUser.InviteL1(userID)).Count(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	c2, err := client.User.Query().Where(entUser.InviteL2(userID)).Count(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	c3, err := client.User.Query().Where(entUser.InviteL3(userID)).Count(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	return int64(c1), int64(c2), int64(c3), nil
}

// ListTeam 下级列表（按层级过滤；tier=0 全部）。
func (r *CommissionRepo) ListTeam(ctx context.Context, userID uint64, tier, page, size int) ([]*ent.User, int, error) {
	q := data.Client(ctx, r.data).User.Query().Order(ent.Desc(entUser.FieldID))
	switch tier {
	case 1:
		q = q.Where(entUser.InviteL1(userID))
	case 2:
		q = q.Where(entUser.InviteL2(userID))
	case 3:
		q = q.Where(entUser.InviteL3(userID))
	default:
		q = q.Where(entUser.Or(
			entUser.InviteL1(userID), entUser.InviteL2(userID), entUser.InviteL3(userID),
		))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}
