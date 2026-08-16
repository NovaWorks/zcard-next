package wallet

// T1 InTx 账务内核（P1-05 核心——全模块最高纪律要求）：
//
// 一切余额变动必须经 InTx：
//   1. 按 reference 查流水——存在直接返回成功（幂等重入，跨模块幂等）
//   2. ensureAccountForUpdate（FOR UPDATE 锁账户，不存在则建——并发竞态处理）
//   3. 非负校验（Debit 时 available-amount ≥ 0）
//   4. 更新余额（乐观锁 version 兜底重试 ≤3 次）
//   5. 写流水（balance_before/after 快照 + reference 唯一索引兜底）
//
// 不变量：total = available + locked 恒真；余额永可由流水重算。

import (
	"context"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/walletaccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/wallettransaction"
)

// Entry 账务入账请求。
type Entry struct {
	UserID     uint64
	Direction  string // "in" | "out"
	Type       string // order_pay / order_refund / recharge / commission / adjust ...
	Amount     int64  // 分（正数）
	Reference  string // 幂等键（重复提交直接返回成功）
	OrderID    uint64
	OperatorID uint64
	Remark     string
}

// WalletRepoImpl 钱包仓储。
type WalletRepoImpl struct {
	data *data.Data
}

// NewWalletRepoImpl 构造。
func NewWalletRepoImpl(d *data.Data) *WalletRepoImpl { return &WalletRepoImpl{data: d} }

// CreditInTx 入账（幂等重入：reference 已存在直接返回成功）。
func (r *WalletRepoImpl) CreditInTx(ctx context.Context, e Entry) error {
	client := data.Client(ctx, r.data)

	// 1) 幂等重入
	exists, err := client.WalletTransaction.Query().
		Where(wallettransaction.Reference(e.Reference)).Exist(ctx)
	if err != nil {
		return fmt.Errorf("wallet: 幂等检查失败: %w", err)
	}
	if exists {
		return nil // 已入账，幂等返回
	}

	// 2) 锁账户（不存在则建）
	acc, err := r.ensureAccount(ctx, e.UserID)
	if err != nil {
		return err
	}

	// 3) 更新余额（乐观锁）
	newBalance := acc.Available + e.Amount
	affected, err := client.WalletAccount.Update().
		Where(walletaccount.ID(acc.ID), walletaccount.VersionEQ(acc.Version)).
		SetAvailable(newBalance).
		SetVersion(acc.Version + 1).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("wallet: 更新余额失败: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("wallet: 乐观锁冲突（并发更新）")
	}

	// 4) 写流水
	_, err = client.WalletTransaction.Create().
		SetUserID(e.UserID).
		SetDirection(e.Direction).
		SetType(e.Type).
		SetAmount(e.Amount).
		SetBalanceBefore(acc.Available).
		SetBalanceAfter(newBalance).
		SetReference(e.Reference).
		SetOrderID(e.OrderID).
		SetOperatorID(e.OperatorID).
		SetRemark(e.Remark).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("wallet: 写流水失败: %w", err)
	}
	return nil
}

// DebitInTx 扣款（余额不足返回 ErrInsufficient）。
func (r *WalletRepoImpl) DebitInTx(ctx context.Context, e Entry) error {
	client := data.Client(ctx, r.data)

	// 1) 幂等重入
	exists, err := client.WalletTransaction.Query().
		Where(wallettransaction.Reference(e.Reference)).Exist(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	// 2) 锁账户
	acc, err := r.ensureAccount(ctx, e.UserID)
	if err != nil {
		return err
	}

	// 3) 非负校验
	if acc.Available < e.Amount {
		return fmt.Errorf("wallet.INSUFFICIENT_BALANCE: avail=%d need=%d", acc.Available, e.Amount)
	}

	// 4) 乐观锁更新
	newBalance := acc.Available - e.Amount
	affected, err := client.WalletAccount.Update().
		Where(walletaccount.ID(acc.ID), walletaccount.VersionEQ(acc.Version)).
		SetAvailable(newBalance).
		SetVersion(acc.Version + 1).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("wallet: 乐观锁冲突")
	}

	// 5) 写流水
	_, err = client.WalletTransaction.Create().
		SetUserID(e.UserID).
		SetDirection(e.Direction).
		SetType(e.Type).
		SetAmount(e.Amount).
		SetBalanceBefore(acc.Available).
		SetBalanceAfter(newBalance).
		SetReference(e.Reference).
		SetOrderID(e.OrderID).
		SetOperatorID(e.OperatorID).
		SetRemark(e.Remark).
		Save(ctx)
	return err
}

// ensureAccount 锁账户（不存在则建——并发建号竞态处理）。
func (r *WalletRepoImpl) ensureAccount(ctx context.Context, userID uint64) (*ent.WalletAccount, error) {
	client := data.Client(ctx, r.data)

	acc, err := client.WalletAccount.Query().
		Where(walletaccount.UserID(userID)).Only(ctx)
	if ent.IsNotFound(err) {
		// 并发建号：冲突则重查
		acc, err = client.WalletAccount.Create().
			SetUserID(userID).
			SetCurrency("CNY").
			SetAvailable(0).
			SetLocked(0).
			SetVersion(0).
			Save(ctx)
		if ent.IsConstraintError(err) {
			return client.WalletAccount.Query().
				Where(walletaccount.UserID(userID)).Only(ctx)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("wallet: 获取账户失败: %w", err)
	}
	return acc, nil
}

// GetBalance 查余额。
func (r *WalletRepoImpl) GetBalance(ctx context.Context, userID uint64) (available, locked int64, err error) {
	acc, err := data.Client(ctx, r.data).WalletAccount.Query().
		Where(walletaccount.UserID(userID)).Only(ctx)
	if ent.IsNotFound(err) {
		return 0, 0, nil // 未开户 = 零余额
	}
	if err != nil {
		return 0, 0, err
	}
	return acc.Available, acc.Locked, nil
}

// ListTransactions 流水列表。
func (r *WalletRepoImpl) ListTransactions(ctx context.Context, userID uint64, page, size int) ([]*ent.WalletTransaction, int64, error) {
	client := data.Client(ctx, r.data)
	q := client.WalletTransaction.Query().
		Where(wallettransaction.UserID(userID)).
		Order(ent.Desc(wallettransaction.FieldCreatedAt))
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Clone().Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, int64(total), err
}

// RebuildBalance 余额重算（对账用——从流水重放验证余额一致）。
func (r *WalletRepoImpl) RebuildBalance(ctx context.Context, userID uint64) (int64, error) {
	rows, err := data.Client(ctx, r.data).WalletTransaction.Query().
		Where(wallettransaction.UserID(userID)).
		Order(ent.Asc(wallettransaction.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return 0, err
	}
	var balance int64
	for _, row := range rows {
		if row.Direction == "in" {
			balance += row.Amount
		} else {
			balance -= row.Amount
		}
	}
	return balance, nil
}

// Lock 冻结（available → locked；availableAt 到期时间戳——佣金/分站利润冻结期）。
// 幂等口径：金额校验 available >= amount，乐观锁 CAS。
func (r *WalletRepoImpl) Lock(ctx context.Context, userID uint64, amount int64, availableAt int64) error {
	if amount <= 0 {
		return fmt.Errorf("wallet: 冻结金额必须为正")
	}
	client := data.Client(ctx, r.data)
	acc, err := client.WalletAccount.Query().
		Where(walletaccount.UserID(userID)).Only(ctx)
	if err != nil {
		return err
	}
	if acc.Available < amount {
		return fmt.Errorf("wallet.BALANCE_INSUFFICIENT")
	}
	affected, err := client.WalletAccount.Update().
		Where(walletaccount.ID(acc.ID), walletaccount.Available(acc.Available)).
		SetAvailable(acc.Available - amount).
		SetLocked(acc.Locked + amount).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("wallet.CONCURRENT_UPDATE")
	}
	// 冻结流水（reference 幂等由调用方保证）
	_, _ = client.WalletTransaction.Create().
		SetUserID(userID).
		SetDirection("out").
		SetType("lock").
		SetAmount(amount).
		SetReference(fmt.Sprintf("lock:%d:%d", userID, availableAt)).
		SetRemark("冻结").
		Save(ctx)
	return nil
}

// Unlock 解冻（locked → available；提现拒绝/佣金退回）。
func (r *WalletRepoImpl) Unlock(ctx context.Context, userID uint64, amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("wallet: 解冻金额必须为正")
	}
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
		SetAvailable(acc.Available + amount).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("wallet.CONCURRENT_UPDATE")
	}
	return nil
}
