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
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/withdrawal"
)

// CommissionConsumer 佣金消耗端口（打款 FIFO；affiliate.CommissionRepo 实现，
// 通道 A——wallet 不直接依赖 affiliate 包）。
type CommissionConsumer interface {
	ConsumeAvailableFIFO(ctx context.Context, userID uint64, amount int64) error
}

// SetCommissionConsumer 装配期注入（wire 后手工/构造器）。
func (r *WalletRepoImpl) SetCommissionConsumer(c CommissionConsumer) { r.commissions = c }

// CreateWithdrawal 提现申请（佣金提现：冻结口径 = pending/approved 提现单金额
// 合计——不锁钱包余额、不逐行锁佣金行；打款时 FIFO 消耗佣金置 withdrawn）。
// 可提校验由 service 层完成（stats.available − frozen ≥ amount）。
func (r *WalletRepoImpl) CreateWithdrawal(ctx context.Context, userID uint64, amount, fee int64, method map[string]any) (*ent.Withdrawal, error) {
	return data.Client(ctx, r.data).Withdrawal.Create().
		SetUserID(userID).
		SetAmount(amount).
		SetFee(fee).
		SetMethod(method).
		SetStatus(withdrawal.StatusPending).
		Save(ctx)
}

// ListWithdrawalsByUser 本人提现记录（按 ID 倒序分页）。
func (r *WalletRepoImpl) ListWithdrawalsByUser(ctx context.Context, userID uint64, page, size int) ([]*ent.Withdrawal, int64, error) {
	q := data.Client(ctx, r.data).Withdrawal.Query().
		Where(withdrawal.UserID(userID)).
		Order(ent.Desc(withdrawal.FieldID))
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, int64(total), err
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

// ReviewWithdrawal 审核：通过→approved；驳回→rejected（佣金冻结口径由提现单
// 状态自动释放——rejected 不再计入 frozen，无需回滚动作）。
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
		// 驳回：冻结口径自动释放（frozen 只统计 pending/approved）
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

// PayWithdrawal 打款（人工打款模式）：approved→paid + 佣金 FIFO 消耗
// （available→withdrawn，末行拆分；线下人工转账不动钱包账户）。
func (r *WalletRepoImpl) PayWithdrawal(ctx context.Context, id uint64, receipt string) (*ent.Withdrawal, error) {
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
		// 佣金 FIFO 消耗（nil = 未装配佣金端口的旧部署，跳过——仅状态流转）
		if r.commissions != nil {
			if err := r.commissions.ConsumeAvailableFIFO(txCtx, w.UserID, w.Amount); err != nil {
				return err
			}
		}
		upd := client.Withdrawal.UpdateOneID(id).
			SetStatus(withdrawal.StatusPaid).
			SetPaidAt(time.Now().UTC())
		if receipt != "" {
			upd.SetReceipt(receipt)
		}
		w, _ = upd.Save(txCtx)
		return nil
	})
	return w, err
}

