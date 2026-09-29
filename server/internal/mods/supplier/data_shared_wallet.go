package supplier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplieraccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplierledgerentry"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/walletaccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/wallettransaction"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

func (r *SupplierRepoImpl) accountBalance(ctx context.Context, acc *ent.SupplierAccount) (int64, error) {
	if !acc.SharedWallet {
		return acc.BalanceCache, nil
	}
	if acc.OwnerUserID == 0 {
		return 0, errors.New("supplier.OWNER_REQUIRED")
	}
	w, err := data.Client(ctx, r.data).WalletAccount.Query().Where(walletaccount.UserID(acc.OwnerUserID)).Only(ctx)
	if ent.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if w.Currency != "CNY" {
		return 0, errors.New("supplier.WALLET_CURRENCY")
	}
	return w.Available, nil
}

// Supplier account lock precedes the user identity lock. The latter is shared
// with market settlement and covers the first wallet creation as well.
func (r *SupplierRepoImpl) lockWalletOwner(ctx context.Context, id uint64) error {
	if id == 0 {
		return errors.New("supplier.OWNER_REQUIRED")
	}
	c := data.Client(ctx, r.data)
	if r.data.Dialect == db.SQLite {
		return c.User.UpdateOneID(id).AddManualLevelID(0).Exec(ctx)
	}
	_, err := c.User.Query().Where(user.ID(id)).ForUpdate().Only(ctx)
	return err
}
func sharedReference(account uint64, reference string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", account, reference)))
	return "supply:" + hex.EncodeToString(h[:])
}
func (r *SupplierRepoImpl) sharedEntry(ctx context.Context, acc *ent.SupplierAccount, typ string, amount int64, reference, remark string) error {
	if amount == 0 || amount == math.MinInt64 || r.wallet == nil {
		return errors.New("supplier.INVALID_SHARED_ENTRY")
	}
	if err := r.lockWalletOwner(ctx, acc.OwnerUserID); err != nil {
		return err
	}
	c := data.Client(ctx, r.data)
	w, err := c.WalletAccount.Query().Where(walletaccount.UserID(acc.OwnerUserID)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return err
	}
	if w != nil && (w.Currency != "CNY" || w.Available < 0 || w.Locked < 0 || w.Version == math.MaxInt32 || w.Locked > math.MaxInt64-w.Available) {
		return errors.New("supplier.INVALID_WALLET")
	}
	if amount > 0 && w != nil && amount > math.MaxInt64-w.Available-w.Locked {
		return errors.New("supplier.BALANCE_OVERFLOW")
	}
	ref := sharedReference(acc.ID, reference)
	// Never turn an unrelated/colliding wallet reference into successful supply.
	exists, err := c.WalletTransaction.Query().Where(wallettransaction.Reference(ref)).Exist(ctx)
	if err != nil {
		return err
	}
	if exists {
		return errors.New("supplier.WALLET_REFERENCE_CONFLICT")
	}
	e := walletport.Entry{UserID: acc.OwnerUserID, Direction: "in", Type: typ, Amount: money.Cents(amount), Reference: ref, Remark: remark}
	if amount > 0 {
		return r.wallet.CreditInTx(ctx, e)
	}
	e.Direction = "out"
	e.Amount = money.Cents(-amount)
	err = r.wallet.DebitInTx(ctx, e)
	if errors.Is(err, walletport.ErrInsufficientBalance) {
		return ErrInsufficientBalance
	}
	return err
}

type WalletMigrationRow struct {
	AccountID     uint64 `json:"account_id"`
	OwnerUserID   uint64 `json:"owner_user_id"`
	Balance       int64  `json:"balance_cents"`
	AlreadyShared bool   `json:"already_shared"`
	Problem       string `json:"problem,omitempty"`
}
type WalletMigrationReport struct {
	Accounts []WalletMigrationRow `json:"accounts"`
	Ready    bool                 `json:"ready"`
	Applied  bool                 `json:"applied"`
}

// MigrateSharedWallet preflights every existing account, including disabled
// accounts, then transfers all or none in one transaction. Explicit mappings
// resolve ownerless legacy accounts only; existing ownership cannot be changed.
// apply=false performs no writes, and apply=true rechecks under account locks.
func (r *SupplierRepoImpl) MigrateSharedWallet(ctx context.Context, owners map[uint64]uint64, apply bool) (WalletMigrationReport, error) {
	report := WalletMigrationReport{Accounts: []WalletMigrationRow{}, Ready: true}
	run := func(ctx context.Context) error {
		c := data.Client(ctx, r.data)
		accounts, err := c.SupplierAccount.Query().Order(ent.Asc(supplieraccount.FieldID)).All(ctx)
		if err != nil {
			return err
		}
		seen := map[uint64]bool{}
		for i, acc := range accounts {
			if apply {
				acc, err = r.lockAccount(ctx, acc.ID)
				if err != nil {
					return err
				}
				accounts[i] = acc
			}
			seen[acc.ID] = true
			row := WalletMigrationRow{AccountID: acc.ID, OwnerUserID: acc.OwnerUserID, Balance: acc.BalanceCache, AlreadyShared: acc.SharedWallet}
			if mapped, ok := owners[acc.ID]; ok {
				if mapped == 0 || (row.OwnerUserID != 0 && row.OwnerUserID != mapped) {
					row.Problem = "owner mapping conflicts with existing ownership"
				} else {
					row.OwnerUserID = mapped
				}
			}
			if row.OwnerUserID == 0 {
				row.Problem = "explicit owner mapping required"
			} else if _, e := c.User.Get(ctx, row.OwnerUserID); e != nil {
				if !ent.IsNotFound(e) {
					return e
				}
				row.Problem = "owner user does not exist"
			}
			if acc.SharedWallet {
				if acc.BalanceCache != 0 {
					row.Problem = "shared account retains legacy balance"
				}
			} else {
				entries, e := c.SupplierLedgerEntry.Query().Where(supplierledgerentry.AccountID(acc.ID)).All(ctx)
				if e != nil {
					return e
				}
				sum := new(big.Int)
				for _, entry := range entries {
					if entry.Currency != "CNY" {
						row.Problem = "legacy currency is not CNY"
					}
					sum.Add(sum, big.NewInt(entry.Amount))
				}
				if !sum.IsInt64() || sum.Int64() != acc.BalanceCache || acc.BalanceCache < 0 {
					row.Problem = "legacy ledger and balance do not reconcile"
				}
			}
			if row.Problem != "" {
				report.Ready = false
			}
			report.Accounts = append(report.Accounts, row)
		}
		for id := range owners {
			if !seen[id] {
				return fmt.Errorf("unknown supplier account in mapping: %d", id)
			}
		}
		// Validate target wallets and the total credits of multiple supplier accounts.
		totals := map[uint64]*big.Int{}
		for _, row := range report.Accounts {
			if row.Problem != "" || row.AlreadyShared {
				continue
			}
			if totals[row.OwnerUserID] == nil {
				totals[row.OwnerUserID] = new(big.Int)
			}
			totals[row.OwnerUserID].Add(totals[row.OwnerUserID], big.NewInt(row.Balance))
		}
		for id, total := range totals {
			w, e := c.WalletAccount.Query().Where(walletaccount.UserID(id)).Only(ctx)
			if e != nil && !ent.IsNotFound(e) {
				return e
			}
			bad := false
			if w != nil {
				bad = w.Currency != "CNY" || w.Available < 0 || w.Locked < 0 || w.Version == math.MaxInt32
				total.Add(total, big.NewInt(w.Available))
				total.Add(total, big.NewInt(w.Locked))
			}
			if bad || !total.IsInt64() {
				report.Ready = false
				for i := range report.Accounts {
					if report.Accounts[i].OwnerUserID == id {
						report.Accounts[i].Problem = "target wallet invalid or combined balance overflows"
					}
				}
			}
		}
		if !apply || !report.Ready {
			return nil
		}
		for i, row := range report.Accounts {
			if row.AlreadyShared {
				continue
			}
			acc := accounts[i]
			acc.OwnerUserID = row.OwnerUserID
			ref := fmt.Sprintf("supply_wallet_migration:%d", acc.ID)
			if row.Balance > 0 {
				if err := r.sharedEntry(ctx, acc, "supply_transfer", row.Balance, ref, "供货旧余额转入账户钱包"); err != nil {
					return err
				}
				if _, err := c.SupplierLedgerEntry.Create().SetAccountID(acc.ID).SetType("wallet_transfer").SetAmount(-row.Balance).SetReference(ref).SetRemark("旧余额转入所属用户账户钱包").Save(ctx); err != nil {
					return err
				}
			}
			if err := c.SupplierAccount.UpdateOneID(acc.ID).SetOwnerUserID(row.OwnerUserID).SetBalanceCache(0).SetSharedWallet(true).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	}
	var err error
	if apply {
		err = data.Tx(ctx, r.data, run)
	} else {
		err = run(ctx)
	}
	if err != nil {
		report.Ready = false
	}
	report.Applied = apply && report.Ready && err == nil
	return report, err
}
