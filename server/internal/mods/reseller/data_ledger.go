package reseller

// 账本补全：提现（FIFO 消费 + 部分拆行 + 幂等）+ 退款逆向（负行 + 负债优先抵扣）。
//
// 账本模型：ledger_entries 行即余额（有符号）；状态流转：
// available → locked（withdraw_lock，部分提现拆行）→ withdrawn（打款）/ available（驳回）
// order.refunded → refund_deduct 负行（available 直接扣减；不足 → pending 负债，
// 后续利润优先抵扣——与 affiliate 负债态同构）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/resellerledgerentry"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// ErrWithdrawLocked 提现锁定冲突（幂等键重放）。
var ErrWithdrawLocked = errors.New("reseller.WITHDRAW_LOCKED: 提现单已锁定")

// WithdrawLock 提现锁定（FIFO 消费 available 正行；行额度不足时拆行）。
// ref 为提现单号（幂等键 withdraw_lock:<ref>；重放直接返回已锁定）。
func (r *ResellerRepo) WithdrawLock(ctx context.Context, subsiteID uint64, amount int64, ref string) error {
	if amount <= 0 || !money.ValidCents(amount) {
		return fmt.Errorf("reseller.INVALID_AMOUNT")
	}
	return data.Tx(ctx, r.data, func(txCtx context.Context) error {
		client := data.Client(txCtx, r.data)
		// 幂等：同 ref 已锁定过 → ACK
		exists, err := client.ResellerLedgerEntry.Query().
			Where(resellerledgerentry.IdempotencyKeyEQ("withdraw_lock:" + ref)).Exist(txCtx)
		if err != nil {
			return err
		}
		if exists {
			return ErrWithdrawLocked
		}
		// FIFO 取 available 正行（先到先得）
		rows, err := client.ResellerLedgerEntry.Query().
			Where(
				resellerledgerentry.SubsiteIDEQ(subsiteID),
				resellerledgerentry.StatusEQ(resellerledgerentry.StatusAvailable),
				resellerledgerentry.AmountGT(0),
			).
			Order(ent.Asc(resellerledgerentry.FieldID)).
			All(txCtx)
		if err != nil {
			return err
		}
		remaining := amount
		now := time.Now().UTC()
		anchorCreated := false
		for _, row := range rows {
			if remaining <= 0 {
				break
			}
			if row.Amount <= remaining {
				// 整行锁定（remark 标记提现单号，打款/驳回按 ref 关联）
				if _, err := client.ResellerLedgerEntry.UpdateOneID(row.ID).
					SetStatus(resellerledgerentry.StatusLocked).
					SetRemark("withdraw_ref:" + ref).
					Save(txCtx); err != nil {
					return err
				}
				remaining -= row.Amount
				continue
			}
			// 部分拆行：原行剩 (行额-剩余)，新建锁定行（amount=剩余）
			if _, err := client.ResellerLedgerEntry.UpdateOneID(row.ID).
				SetAmount(row.Amount - remaining).
				Save(txCtx); err != nil {
				return err
			}
			if _, err := client.ResellerLedgerEntry.Create().
				SetSubsiteID(subsiteID).
				SetType("withdraw_lock").
				SetAmount(remaining).
				SetStatus(resellerledgerentry.StatusLocked).
				SetIdempotencyKey("withdraw_lock:" + ref).
				SetCreatedAt(now).
				SetRemark("withdraw_ref:" + ref).
				Save(txCtx); err != nil {
				return err
			}
			anchorCreated = true
			remaining = 0
		}
		if remaining > 0 {
			return fmt.Errorf("reseller.INSUFFICIENT_BALANCE")
		}
		// 无拆行时也需幂等锚点行（标记提现单已锁定；与拆行幂等键互斥）
		if !anchorCreated {
			if _, err := client.ResellerLedgerEntry.Create().
				SetSubsiteID(subsiteID).
				SetType("withdraw_lock").
				SetAmount(0).
				SetStatus(resellerledgerentry.StatusLocked).
				SetIdempotencyKey("withdraw_lock:" + ref).
				SetCreatedAt(now).
				SetRemark("withdraw_ref:" + ref).
				Save(txCtx); err != nil {
				return err
			}
		}
		// 缓存：available -amount / locked +amount
		return r.updateBalance(txCtx, subsiteID, -amount, amount, 0)
	})
}

// WithdrawPaid 打款（锁定行 → withdrawn；金额从 locked 出账）。
func (r *ResellerRepo) WithdrawPaid(ctx context.Context, subsiteID uint64, ref string) error {
	client := data.Client(ctx, r.data)
	rows, err := client.ResellerLedgerEntry.Query().
		Where(
			resellerledgerentry.SubsiteIDEQ(subsiteID),
			resellerledgerentry.StatusEQ(resellerledgerentry.StatusLocked),
			resellerledgerentry.RemarkHasPrefix("withdraw_ref:"+ref),
		).All(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("reseller.WITHDRAW_NOT_LOCKED")
	}
	var paidTotal int64
	for _, row := range rows {
		if row.Amount == 0 {
			continue // 锚点行
		}
		if _, err := client.ResellerLedgerEntry.UpdateOneID(row.ID).
			SetStatus(resellerledgerentry.StatusWithdrawn).
			Save(ctx); err != nil {
			return err
		}
		paidTotal += row.Amount
	}
	return r.updateBalance(ctx, subsiteID, 0, -paidTotal, 0)
}

// WithdrawReject 驳回（锁定行 → available 回余额）。
func (r *ResellerRepo) WithdrawReject(ctx context.Context, subsiteID uint64, ref string) error {
	client := data.Client(ctx, r.data)
	rows, err := client.ResellerLedgerEntry.Query().
		Where(
			resellerledgerentry.SubsiteIDEQ(subsiteID),
			resellerledgerentry.StatusEQ(resellerledgerentry.StatusLocked),
			resellerledgerentry.RemarkHasPrefix("withdraw_ref:"+ref),
		).All(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("reseller.WITHDRAW_NOT_LOCKED")
	}
	var rejectTotal int64
	for _, row := range rows {
		status := resellerledgerentry.StatusAvailable
		if row.Amount == 0 {
			status = resellerledgerentry.StatusWithdrawn // 锚点行直接作废
		} else {
			rejectTotal += row.Amount
		}
		if _, err := client.ResellerLedgerEntry.UpdateOneID(row.ID).
			SetStatus(status).
			Save(ctx); err != nil {
			return err
		}
	}
	return r.updateBalance(ctx, subsiteID, rejectTotal, -rejectTotal, 0)
}

// RefundDeduct 退款扣回（幂等键 refund_deduct:<orderID>）。
// ratio 万分比（0=全额）。余额不足 → 负行转 pending 负债。
func (r *ResellerRepo) RefundDeduct(ctx context.Context, subsiteID, orderID uint64, profit int64, ratio int64) error {
	if ratio <= 0 {
		ratio = 10000
	}
	clawback := profit * ratio / 10000
	if clawback <= 0 {
		return nil
	}
	return data.Tx(ctx, r.data, func(txCtx context.Context) error {
		client := data.Client(txCtx, r.data)
		exists, err := client.ResellerLedgerEntry.Query().
			Where(resellerledgerentry.IdempotencyKeyEQ(fmt.Sprintf("refund_deduct:%d", orderID))).Exist(txCtx)
		if err != nil {
			return err
		}
		if exists {
			return nil // 幂等 ACK
		}
		// 可用口径（available + pending 正数）累计
		availRows, err := client.ResellerLedgerEntry.Query().
			Where(
				resellerledgerentry.SubsiteIDEQ(subsiteID),
				resellerledgerentry.StatusEQ(resellerledgerentry.StatusAvailable),
				resellerledgerentry.AmountGT(0),
			).
			Order(ent.Asc(resellerledgerentry.FieldID)).
			All(txCtx)
		if err != nil {
			return err
		}
		var availTotal int64
		for _, row := range availRows {
			availTotal += row.Amount
		}
		now := time.Now().UTC()
		if availTotal >= clawback {
			// 足额：FIFO 逐行扣减
			remaining := clawback
			for _, row := range availRows {
				if remaining <= 0 {
					break
				}
				if row.Amount <= remaining {
					if _, err := client.ResellerLedgerEntry.UpdateOneID(row.ID).
						SetStatus(resellerledgerentry.StatusWithdrawn).
						Save(txCtx); err != nil {
						return err
					}
					remaining -= row.Amount
					continue
				}
				if _, err := client.ResellerLedgerEntry.UpdateOneID(row.ID).
					SetAmount(row.Amount - remaining).
					Save(txCtx); err != nil {
					return err
				}
				remaining = 0
			}
			// 扣回记录行（负）
			if _, err := client.ResellerLedgerEntry.Create().
				SetSubsiteID(subsiteID).
				SetOrderID(orderID).
				SetType("refund_deduct").
				SetAmount(-clawback).
				SetStatus(resellerledgerentry.StatusWithdrawn).
				SetIdempotencyKey(fmt.Sprintf("refund_deduct:%d", orderID)).
				SetCreatedAt(now).
				SetRemark("退款利润扣回").
				Save(txCtx); err != nil {
				return err
			}
			return r.updateBalance(txCtx, subsiteID, -clawback, 0, 0)
		}
		// 不足：负债态（负行 pending，后续利润优先抵扣）
		if _, err := client.ResellerLedgerEntry.Create().
			SetSubsiteID(subsiteID).
			SetOrderID(orderID).
			SetType("refund_deduct").
			SetAmount(-(clawback - availTotal)).
			SetStatus(resellerledgerentry.StatusPending).
			SetIdempotencyKey(fmt.Sprintf("refund_deduct:%d", orderID)).
			SetCreatedAt(now).
			SetRemark("退款利润扣回（负债，待利润抵扣）").
			Save(txCtx); err != nil {
			return err
		}
		// 足额部分照扣
		if availTotal > 0 {
			remaining := availTotal
			for _, row := range availRows {
				if remaining <= 0 {
					break
				}
				if row.Amount <= remaining {
					if _, err := client.ResellerLedgerEntry.UpdateOneID(row.ID).
						SetStatus(resellerledgerentry.StatusWithdrawn).
						Save(txCtx); err != nil {
						return err
					}
					remaining -= row.Amount
					continue
				}
				if _, err := client.ResellerLedgerEntry.UpdateOneID(row.ID).
					SetAmount(row.Amount - remaining).
					Save(txCtx); err != nil {
					return err
				}
				remaining = 0
			}
		}
		// 缓存：available 扣减已扣部分；负债增差额
		return r.updateBalance(txCtx, subsiteID, -availTotal, 0, clawback-availTotal)
	})
}

// settleDebt 利润入账前优先抵扣 pending 负行（负债态）。
// 返回抵扣后剩余可入账金额。
func (r *ResellerRepo) settleDebt(ctx context.Context, subsiteID uint64, amount int64) (int64, error) {
	client := data.Client(ctx, r.data)
	rows, err := client.ResellerLedgerEntry.Query().
		Where(
			resellerledgerentry.SubsiteIDEQ(subsiteID),
			resellerledgerentry.StatusEQ(resellerledgerentry.StatusPending),
			resellerledgerentry.AmountLT(0),
		).
		Order(ent.Asc(resellerledgerentry.FieldID)).
		All(ctx)
	if err != nil {
		return amount, err
	}
	remaining := amount
	for _, row := range rows {
		if remaining <= 0 {
			break
		}
		debt := -row.Amount
		if debt <= remaining {
			// 负债清空
			if _, err := client.ResellerLedgerEntry.UpdateOneID(row.ID).
				SetStatus(resellerledgerentry.StatusWithdrawn).
				Save(ctx); err != nil {
				return amount, err
			}
			remaining -= debt
			continue
		}
		// 部分抵扣：负债减少
		if _, err := client.ResellerLedgerEntry.UpdateOneID(row.ID).
			SetAmount(-(debt - remaining)).
			Save(ctx); err != nil {
			return amount, err
		}
		remaining = 0
	}
	// 负债缓存随抵扣减少（由调用方在入账时一并 updateBalance）
	return remaining, nil
}

var _ = json.Marshal
