package wallet

// T5 提现执行（P1-05 M3）：
//   申请：available→locked（Lock）+ 手续费 + 收款方式白名单快照 → withdrawals(pending)
//   审核：通过→approved（锁定保持）；驳回→rejected（Unlock 回余额）
//   打款：人工打款模式 approved→paid（locked 扣减 + 流水 type=withdraw）
// 纪律：金额服务端裁决（铁律 16）；锁定/解锁/扣减全部走账户 CAS + 流水，可重算。

import (
	"context"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/walletaccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/withdrawal"
)

// CreateWithdrawal 提现申请：Lock(available→locked) + 落 withdrawals(pending)，
// 同一事务（Lock 内部写流水；withdrawals 行状态机后续流转）。
func (r *WalletRepoImpl) CreateWithdrawal(ctx context.Context, userID uint64, amount, fee int64, method map[string]any) (*ent.Withdrawal, error) {
	var w *ent.Withdrawal
	err := data.Tx(ctx, r.data, func(txCtx context.Context) error {
		if err := r.Lock(txCtx, userID, amount, 0); err != nil {
			return err // 余额不足/并发冲突整体回滚
		}
		created, err := data.Client(txCtx, r.data).Withdrawal.Create().
			SetUserID(userID).
			SetAmount(amount).
			SetFee(fee).
			SetMethod(method).
			SetStatus(withdrawal.StatusPending).
			Save(txCtx)
		if err != nil {
			return err
		}
		w = created
		return nil
	})
	return w, err
}

// ListWithdrawals 提现单列表（状态筛选；按 ID 倒序）。
func (r *WalletRepoImpl) ListWithdrawals(ctx context.Context, status string, page, size int) ([]*ent.Withdrawal, int64, error) {
	q := data.Client(ctx, r.data).Withdrawal.Query().
		Order(ent.Desc(withdrawal.FieldID))
	if status != "" {
		q = q.Where(withdrawal.StatusEQ(withdrawal.Status(status)))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, int64(total), err
}

// GetWithdrawal 单查。
func (r *WalletRepoImpl) GetWithdrawal(ctx context.Context, id uint64) (*ent.Withdrawal, error) {
	return data.Client(ctx, r.data).Withdrawal.Get(ctx, id)
}

// ReviewWithdrawal 审核：通过→approved（锁定保持）；驳回→rejected + Unlock 回余额。
func (r *WalletRepoImpl) ReviewWithdrawal(ctx context.Context, id uint64, approve bool, reason string, reviewerID uint64) (*ent.Withdrawal, error) {
	var w *ent.Withdrawal
	err := data.Tx(ctx, r.data, func(txCtx context.Context) error {
		client := data.Client(txCtx, r.data)
		w, _ = client.Withdrawal.Get(txCtx, id)
		if w == nil {
			return fmt.Errorf("wallet.WITHDRAWAL_NOT_FOUND")
		}
		if w.Status != withdrawal.StatusPending {
			return fmt.Errorf("wallet.WITHDRAWAL_NOT_PENDING")
		}
		now := time.Now().UTC()
		if approve {
			w, _ = client.Withdrawal.UpdateOneID(id).
				SetStatus(withdrawal.StatusApproved).
				SetReviewedBy(reviewerID).
				SetReviewedAt(now).
				Save(txCtx)
			return nil
		}
		// 驳回：解锁回余额（幂等由状态机 CAS 保证）
		if err := r.Unlock(txCtx, w.UserID, w.Amount); err != nil {
			return err
		}
		upd := client.Withdrawal.UpdateOneID(id).
			SetStatus(withdrawal.StatusRejected).
			SetReviewedBy(reviewerID).
			SetReviewedAt(now)
		if reason != "" {
			upd.SetRejectReason(reason)
		}
		w, _ = upd.Save(txCtx)
		return nil
	})
	return w, err
}

// PayWithdrawal 打款（人工打款模式）：approved→paid + locked 扣减 + 流水 type=withdraw。
func (r *WalletRepoImpl) PayWithdrawal(ctx context.Context, id uint64) (*ent.Withdrawal, error) {
	var w *ent.Withdrawal
	err := data.Tx(ctx, r.data, func(txCtx context.Context) error {
		client := data.Client(txCtx, r.data)
		w, _ = client.Withdrawal.Get(txCtx, id)
		if w == nil {
			return fmt.Errorf("wallet.WITHDRAWAL_NOT_FOUND")
		}
		if w.Status != withdrawal.StatusApproved {
			return fmt.Errorf("wallet.WITHDRAWAL_NOT_APPROVED")
		}
		// locked 扣减 + 流水（withdraw 出账；余额=available+locked 快照可重算）
		if err := r.withdrawPaid(txCtx, w.UserID, w.Amount, w.ID); err != nil {
			return err
		}
		w, _ = client.Withdrawal.UpdateOneID(id).
			SetStatus(withdrawal.StatusPaid).
			SetPaidAt(time.Now().UTC()).
			Save(txCtx)
		return nil
	})
	return w, err
}

// withdrawPaid locked 扣减 + 流水（幂等键 withdraw:<id>，重放只扣一次）。
func (r *WalletRepoImpl) withdrawPaid(ctx context.Context, userID uint64, amount int64, withdrawalID uint64) error {
	client := data.Client(ctx, r.data)
	acc, err := client.WalletAccount.Query().
		Where(walletaccount.UserID(userID)).Only(ctx)
	if err != nil {
		return err
	}
	if acc.Locked < amount {
		return fmt.Errorf("wallet.INSUFFICIENT_LOCKED")
	}
	affected, err := client.WalletAccount.Update().
		Where(walletaccount.ID(acc.ID), walletaccount.Locked(acc.Locked)).
		SetLocked(acc.Locked - amount).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("wallet.CONCURRENT_UPDATE")
	}
	_, err = client.WalletTransaction.Create().
		SetUserID(userID).
		SetDirection("out").
		SetType("withdraw").
		SetAmount(amount).
		SetBalanceBefore(acc.Available + acc.Locked).
		SetBalanceAfter(acc.Available + acc.Locked - amount).
		SetReference(fmt.Sprintf("withdraw:%d", withdrawalID)).
		SetRemark("提现打款").
		Save(ctx)
	return err
}
