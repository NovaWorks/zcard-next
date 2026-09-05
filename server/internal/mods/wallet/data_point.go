package wallet

// 积分账本（ ，与余额 InTx 同构）：
// point_accounts(balance, version) + point_transactions(reference 幂等键)；
// 非负校验 + 乐观锁 CAS + 流水快照——余额永可由流水重算。
// 产生口径：充值赠送（earn_recharge）/消费（earn_consume）/兑换（redeem）/调账（adjust）。

import (
	"context"
	"fmt"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pointaccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pointtransaction"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/wallettransaction"
)

// PointEntry 积分入账请求。
type PointEntry struct {
	UserID    uint64
	Direction string // "in" | "out"
	Type      string // earn_recharge / earn_consume / redeem / adjust ...
	Amount    int64  // 正数
	Reference string // 幂等键（points:<orderID> 等；重复提交直接返回成功）
	OrderID   uint64
	Remark    string
}

// PointCreditInTx 积分入账（幂等重入：reference 已存在直接返回成功）。
func (r *WalletRepoImpl) PointCreditInTx(ctx context.Context, e PointEntry) error {
	if e.Amount <= 0 {
		return fmt.Errorf("wallet: 积分必须为正")
	}
	return data.Tx(ctx, r.data, func(txCtx context.Context) error {
		client := data.Client(txCtx, r.data)
		// 幂等：reference 已存在 → ACK
		exists, err := client.PointTransaction.Query().
			Where(pointtransaction.ReferenceEQ(e.Reference)).Exist(txCtx)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		acc, err := client.PointAccount.Query().
			Where(pointaccount.UserID(e.UserID)).Only(txCtx)
		if ent.IsNotFound(err) {
			acc, err = client.PointAccount.Create().
				SetUserID(e.UserID).SetBalance(0).SetVersion(0).Save(txCtx)
		}
		if err != nil {
			return err
		}
		before := acc.Balance
		after := before + e.Amount
		affected, err := client.PointAccount.Update().
			Where(pointaccount.ID(acc.ID), pointaccount.Version(acc.Version)).
			SetBalance(after).SetVersion(acc.Version + 1).
			Save(txCtx)
		if err != nil {
			return err
		}
		if affected == 0 {
			return fmt.Errorf("wallet.CONCURRENT_UPDATE")
		}
		_, err = client.PointTransaction.Create().
			SetUserID(e.UserID).SetDirection(e.Direction).SetType(e.Type).
			SetAmount(e.Amount).SetBalanceBefore(before).SetBalanceAfter(after).
			SetReference(e.Reference).SetNillableOrderID(nilOrZero(e.OrderID)).
			SetRemark(e.Remark).
			Save(txCtx)
		return err
	})
}

// PointDebitInTx 积分扣减（非负校验；不足拒绝不产生流水）。
func (r *WalletRepoImpl) PointDebitInTx(ctx context.Context, e PointEntry) error {
	if e.Amount <= 0 {
		return fmt.Errorf("wallet: 积分必须为正")
	}
	return data.Tx(ctx, r.data, func(txCtx context.Context) error {
		client := data.Client(txCtx, r.data)
		exists, err := client.PointTransaction.Query().
			Where(pointtransaction.ReferenceEQ(e.Reference)).Exist(txCtx)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		acc, err := client.PointAccount.Query().
			Where(pointaccount.UserID(e.UserID)).Only(txCtx)
		if ent.IsNotFound(err) || acc.Balance < e.Amount {
			return fmt.Errorf("wallet.POINTS_INSUFFICIENT")
		}
		if err != nil {
			return err
		}
		before := acc.Balance
		after := before - e.Amount
		affected, err := client.PointAccount.Update().
			Where(pointaccount.ID(acc.ID), pointaccount.Version(acc.Version)).
			SetBalance(after).SetVersion(acc.Version + 1).
			Save(txCtx)
		if err != nil {
			return err
		}
		if affected == 0 {
			return fmt.Errorf("wallet.CONCURRENT_UPDATE")
		}
		_, err = client.PointTransaction.Create().
			SetUserID(e.UserID).SetDirection(e.Direction).SetType(e.Type).
			SetAmount(e.Amount).SetBalanceBefore(before).SetBalanceAfter(after).
			SetReference(e.Reference).SetNillableOrderID(nilOrZero(e.OrderID)).
			SetRemark(e.Remark).
			Save(txCtx)
		return err
	})
}

// GetPoints 积分余额（账户不存在返回 0）。
func (r *WalletRepoImpl) GetPoints(ctx context.Context, userID uint64) (int64, error) {
	acc, err := data.Client(ctx, r.data).PointAccount.Query().
		Where(pointaccount.UserID(userID)).Only(ctx)
	if ent.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return acc.Balance, nil
}

// CumulativeRecharge 累计充值（countAsRecharge 口径：仅 type=recharge 入账流水；
// 等级阈值消费——互转/调账/佣金/退款均不计，防刷）。
func (r *WalletRepoImpl) CumulativeRecharge(ctx context.Context, userID uint64) (int64, error) {
	sum, err := data.Client(ctx, r.data).WalletTransaction.Query().
		Where(
			wallettransaction.UserID(userID),
			wallettransaction.Direction("in"),
			wallettransaction.TypeEQ("recharge"),
		).
		Aggregate(ent.Sum(wallettransaction.FieldAmount)).Int(ctx)
	// SUM 空集返回 NULL（scan 失败）——口径为 0
	if err != nil {
		if strings.Contains(err.Error(), "NULL") || strings.Contains(err.Error(), "Scan") {
			return 0, nil
		}
		return 0, err
	}
	return int64(sum), nil
}

// RebuildPoints 积分重算（对账函数——测试断言口径）。
func (r *WalletRepoImpl) RebuildPoints(ctx context.Context, userID uint64) (int64, error) {
	rows, err := data.Client(ctx, r.data).PointTransaction.Query().
		Where(pointtransaction.UserID(userID)).All(ctx)
	if err != nil {
		return 0, err
	}
	var sum int64
	for _, t := range rows {
		if t.Direction == "in" {
			sum += t.Amount
		} else {
			sum -= t.Amount
		}
	}
	return sum, nil
}

func nilOrZero(v uint64) *uint64 {
	if v == 0 {
		return nil
	}
	return &v
}
