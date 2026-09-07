package migratev1

// P5 资金域迁移：bills → wallet_transactions（重放 + 余额三方对账差异清单）、
// withdrawals、commissions → affiliate_commissions。
// 映射规格《数据迁移工具开发计划》§5.6。
//
// 对账不变量（差异暴露而非修复）：每用户末条流水 balance_after ==
// wallet_accounts.available（P1 的 1.x users.balance 快照）；不等即进
// 「资金差异清单」——1.x 账实不符是历史问题，迁移工具的职责是暴露它。

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/affiliatecommission"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/wallettransaction"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/withdrawal"
)

// MigrateMoney P5 阶段。
func (m *Migrator) MigrateMoney(ctx context.Context) error {
	if err := m.migrateBills(ctx); err != nil {
		return err
	}
	if err := m.migrateWithdrawals(ctx); err != nil {
		return err
	}
	if err := m.migrateCommissions(ctx); err != nil {
		return err
	}
	// bills 全量迁完后做余额三方对账（差异进清单不修复）
	return m.ReconcileWallets(ctx)
}

// migrateBills 1.x bills → wallet_transactions（重放）。
// direction：type 1=收入→in、0=支出→out；balance_before 由 balance_after 反推；
// reference = v1_import:bills:<old_id>（幂等键，重放语义）。type 用 v1_bill 标记来源。
func (m *Migrator) migrateBills(ctx context.Context) error {
	var (
		id, userID   int64
		amount       int64
		balanceAfter sql.NullInt64
		typ          int64
		log          sql.NullString
		orderID      sql.NullInt64
		adminID      sql.NullInt64
		createdAt    sql.NullString
	)
	return m.scanTable(ctx, "bills",
		[]string{"id", "user_id", "amount", "balance_after", "type", "log", "order_id", "admin_id", "created_at"},
		func() []any {
			return []any{&id, &userID, &amount, &balanceAfter, &typ, &log, &orderID, &adminID, &createdAt}
		},
		func(int64) error {
			reference := fmt.Sprintf("v1_import:bills:%d", id)
			exists, err := m.Client.WalletTransaction.Query().
				Where(wallettransaction.Reference(reference)).Exist(ctx)
			if err != nil {
				return err
			}
			if exists {
				m.st.Record("bills", "skip")
				return nil
			}
			newU, ok := m.IDs.Get(ctx, "users", uint64(userID))
			if !ok {
				m.st.Record("bills", "skip")
				return nil
			}
			// 收入：after = before + amount → before = after - amount；
			// 支出：after = before - amount → before = after + amount
			direction, before := "out", nullInt(balanceAfter)+amount
			if typ == 1 {
				direction, before = "in", nullInt(balanceAfter)-amount
			}
			b := m.Client.WalletTransaction.Create().
				SetUserID(newU).
				SetDirection(direction).
				SetType("v1_bill").
				SetAmount(amount).
				SetBalanceBefore(before).
				SetBalanceAfter(nullInt(balanceAfter)).
				SetReference(reference)
			if l := nullStr(log); l != "" {
				b.SetRemark(l)
			}
			if o := nullInt(orderID); o > 0 {
				if newO, ok := m.IDs.Get(ctx, "orders", uint64(o)); ok {
					b.SetOrderID(newO)
				}
			}
			if a := nullInt(adminID); a > 0 {
				if newA, ok := m.IDs.Get(ctx, "admin_users", uint64(a)); ok {
					b.SetOperatorID(newA)
				}
			}
			if t, ok, err := mustTime(nullStr(createdAt), m.TZ); err != nil {
				return err
			} else if ok {
				b.SetCreatedAt(t)
			}
			if _, err := b.Save(ctx); err != nil {
				return err
			}
			m.st.Record("bills", "migrated")
			return nil
		},
	)
}

// ReconcileWallets 余额三方对账：每用户「末条流水 balance_after vs 钱包快照 available
// （=1.x users.balance）」。差异进清单（errors.jsonl + 报告），不修复——切换前人工核销。
// 必须在 bills 全量迁完后调用。
func (m *Migrator) ReconcileWallets(ctx context.Context) error {
	if m.dry {
		return nil
	}
	accounts, err := m.Client.WalletAccount.Query().All(ctx)
	if err != nil {
		return err
	}
	t := m.st.table("wallet_reconcile")
	var diff int64
	for _, acc := range accounts {
		last, err := m.Client.WalletTransaction.Query().
			Where(wallettransaction.UserID(acc.UserID)).
			Order(ent.Desc(wallettransaction.FieldCreatedAt), ent.Desc(wallettransaction.FieldID)).
			First(ctx)
		if ent.IsNotFound(err) {
			continue // 无流水（空账户）：快照即真相
		}
		if err != nil {
			return err
		}
		if last.BalanceAfter != acc.Available {
			diff++
			m.RW.AddError("wallet_reconcile", acc.UserID,
				fmt.Sprintf("账实不符：末条流水 balance_after=%d ≠ 钱包快照 available=%d（差 %d 分）",
					last.BalanceAfter, acc.Available, last.BalanceAfter-acc.Available))
		}
	}
	t.SkippedExists = diff
	t.Migrated = int64(len(accounts)) - diff
	return nil
}

// migrateWithdrawals 1.x withdrawals → withdrawals。
// method/account/account_name 合成 2.0 method JSON；approved（1.x=已打款）→ paid。
func (m *Migrator) migrateWithdrawals(ctx context.Context) error {
	var (
		id, userID, amount, fee int64
		actualAmount            sql.NullInt64
		method                  string
		account, accountName    sql.NullString
		status                  string
		rejectReason            sql.NullString
		adminID                 sql.NullInt64
		processedAt, createdAt  sql.NullString
	)
	return m.scanTable(ctx, "withdrawals",
		[]string{"id", "user_id", "amount", "actual_amount", "fee", "method",
			"account", "account_name", "status", "reject_reason", "admin_id", "processed_at", "created_at"},
		func() []any {
			return []any{&id, &userID, &amount, &actualAmount, &fee, &method,
				&account, &accountName, &status, &rejectReason, &adminID, &processedAt, &createdAt}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "withdrawals", uint64(id)); ok {
				m.st.Record("withdrawals", "skip")
				return nil
			}
			newU, ok := m.IDs.Get(ctx, "users", uint64(userID))
			if !ok {
				m.st.Record("withdrawals", "skip")
				return nil
			}
			st := "pending"
			switch status {
			case "approved":
				st = "paid" // 1.x approved=已打款（有 processed_at）
			case "rejected":
				st = "rejected"
			}
			methodJSON := map[string]any{
				"channel": method, // alipay/wechat/usdt
				"account": nullStr(account),
				"name":    nullStr(accountName),
			}
			b := m.Client.Withdrawal.Create().
				SetUserID(newU).
				SetAmount(amount).
				SetFee(fee).
				SetMethod(methodJSON).
				SetStatus(withdrawal.Status(st))
			if t, ok, err := mustTime(nullStr(processedAt), m.TZ); err != nil {
				return err
			} else if ok {
				b.SetPaidAt(t)
			}
			if t, ok, _ := mustTime(nullStr(processedAt), m.TZ); ok {
				b.SetReviewedAt(t)
			}
			if a := nullInt(adminID); a > 0 {
				if newA, ok := m.IDs.Get(ctx, "admin_users", uint64(a)); ok {
					b.SetReviewedBy(newA)
				}
			}
			if r := nullStr(rejectReason); r != "" {
				b.SetRejectReason(r)
			}
			if t, ok, _ := mustTime(nullStr(createdAt), m.TZ); ok {
				b.SetCreatedAt(t)
			}
			w, err := b.Save(ctx)
			if err != nil {
				return err
			}
			if aa := nullInt(actualAmount); aa != 0 && aa != amount-fee {
				m.RW.AddError("withdrawals", uint64(id),
					fmt.Sprintf("actual_amount=%d 与 amount-fee=%d 不一致（2.0 无独立到账列，差异记录在案）", aa, amount-fee))
			}
			if _, err := m.IDs.Put(ctx, m.Client, "withdrawals", uint64(id), w.ID); err != nil {
				return err
			}
			m.st.Record("withdrawals", "migrated")
			return nil
		},
	)
}

// migrateCommissions 1.x commissions → affiliate_commissions。
// pending（1.x 冻结中）→ pending_confirm；paid（已随提现支付）→ withdrawn。
func (m *Migrator) migrateCommissions(ctx context.Context) error {
	var (
		id, buyerID, referrerID int64
		orderID                 sql.NullInt64
		tier                    int64
		rate                    sql.NullFloat64
		baseAmount, amount      int64
		status                  string
		createdAt               sql.NullString
	)
	return m.scanTable(ctx, "commissions",
		[]string{"id", "order_id", "buyer_id", "referrer_id", "tier", "rate",
			"base_amount", "amount", "status", "created_at"},
		func() []any {
			return []any{&id, &orderID, &buyerID, &referrerID, &tier, &rate,
				&baseAmount, &amount, &status, &createdAt}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "commissions", uint64(id)); ok {
				m.st.Record("commissions", "skip")
				return nil
			}
			newR, ok := m.IDs.Get(ctx, "users", uint64(referrerID))
			if !ok {
				m.st.Record("commissions", "skip")
				return nil
			}
			st := "available"
			switch status {
			case "pending":
				st = "pending_confirm" // 历史冻结佣金折算冻结态（§8.2 口径）
			case "paid":
				st = "withdrawn"
			}
			b := m.Client.AffiliateCommission.Create().
				SetOrderID(0). // 1.x 订单未映射时置 0（下方尝试）
				SetReferrerID(newR).
				SetTier(int8(tier)).
				SetBaseAmount(baseAmount).
				SetAmount(amount).
				SetStatus(affiliatecommission.Status(st))
			if rate := nullFloat(rate); rate != 0 {
				b.SetRate(rate)
			}
			if o := nullInt(orderID); o > 0 {
				if newO, ok := m.IDs.Get(ctx, "orders", uint64(o)); ok {
					b.SetOrderID(newO)
				}
			}
			if buyerID > 0 {
				if newB, ok := m.IDs.Get(ctx, "users", uint64(buyerID)); ok {
					b.SetBuyerID(newB)
				}
			}
			if t, ok, _ := mustTime(nullStr(createdAt), m.TZ); ok {
				b.SetCreatedAt(t)
				if st == "available" {
					b.SetAvailableAt(t) // available_at 空 → 迁移时刻（即时可用语义）
				}
			} else if st == "available" {
				b.SetAvailableAt(time.Now().UTC())
			}
			c, err := b.Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "commissions", uint64(id), c.ID); err != nil {
				return err
			}
			m.st.Record("commissions", "migrated")
			return nil
		},
	)
}
