package supplier

import (
	"context"
	"errors"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplierledgerentry"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
)

func TestLegacyWalletMigrationAndSharedPayments(t *testing.T) {
	r, d := newSupplierTestData(t)
	exerciseSharedWallet(t, r, d)
}
func exerciseSharedWallet(t *testing.T, r *SupplierRepoImpl, d *data.Data) {
	ctx := context.Background()
	u := d.Client.User.Create().SetUsername("shared-owner").SaveX(ctx)
	id := seedAccount(t, r)
	w := wallet.NewWalletRepoImpl(d)
	if err := data.Tx(ctx, d, func(ctx context.Context) error {
		return w.CreditInTx(ctx, wallet.Entry{UserID: u.ID, Direction: "in", Type: "recharge", Amount: 3000, Reference: "initial-wallet"})
	}); err != nil {
		t.Fatal(err)
	}
	report, err := r.MigrateSharedWallet(ctx, nil, true)
	if err != nil || report.Ready || report.Applied {
		t.Fatalf("orphan accepted: %+v %v", report, err)
	}
	if b, _ := r.BalanceOf(ctx, id); b != 10000 {
		t.Fatal("failed preflight modified balance", b)
	}
	owners := map[uint64]uint64{id: u.ID}
	report, err = r.MigrateSharedWallet(ctx, owners, false)
	if err != nil || !report.Ready || report.Applied {
		t.Fatalf("preflight: %+v %v", report, err)
	}
	if b, _, _ := w.GetBalance(ctx, u.ID); b != 3000 {
		t.Fatal("preflight credited wallet", b)
	}
	for range 2 {
		report, err = r.MigrateSharedWallet(ctx, owners, true)
		if err != nil || !report.Applied {
			t.Fatalf("migration: %+v %v", report, err)
		}
	}
	if b, _ := r.BalanceOf(ctx, id); b != 13000 {
		t.Fatal("migration doubled/lost money", b)
	}
	if got := d.Client.SupplierAccount.GetX(ctx, id); got.BalanceCache != 0 || !got.SharedWallet {
		t.Fatal(got)
	}
	// A plugin purchase uses this same account wallet, visible to supply immediately.
	if err := data.Tx(ctx, d, func(ctx context.Context) error {
		return w.DebitInTx(ctx, wallet.Entry{UserID: u.ID, Direction: "out", Type: "market_purchase", Amount: 4000, Reference: "market_purchase:test"})
	}); err != nil {
		t.Fatal(err)
	}
	if b, _ := r.BalanceOf(ctx, id); b != 9000 {
		t.Fatal(b)
	}
	if err := r.LedgerEntry(ctx, id, 5, "supply_pay", -5000, "supply:pay:test", "test"); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := w.GetBalance(ctx, u.ID); b != 4000 {
		t.Fatal(b)
	}
	if err := r.LedgerEntry(ctx, id, 5, "supply_pay", -5000, "supply:pay:test", "test"); !errors.Is(err, ErrDuplicateLedger) {
		t.Fatal(err)
	}
	if err := r.LedgerEntry(ctx, id, 6, "supply_pay", -4001, "supply:pay:insufficient", "test"); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatal(err)
	}
	if n := d.Client.SupplierLedgerEntry.Query().Where(supplierledgerentry.Reference("supply:pay:insufficient")).CountX(ctx); n != 0 {
		t.Fatal("failed debit wrote audit")
	}
	if err := r.LedgerEntry(ctx, id, 5, "supply_refund", 5000, "supply:refund:test", "test"); err != nil {
		t.Fatal(err)
	}
	if err := r.Recharge(ctx, id, 1000, "supply:recharge:after", "test"); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := w.GetBalance(ctx, u.ID); b != 10000 {
		t.Fatal(b)
	}
	rebuilt, err := w.RebuildBalance(ctx, u.ID)
	if err != nil || rebuilt != 10000 {
		t.Fatal(rebuilt, err)
	}
}

func TestWalletMigrationRejectsMismatchAndRollsBackAll(t *testing.T) {
	r, d := newSupplierTestData(t)
	ctx := context.Background()
	u := d.Client.User.Create().SetUsername("owner").SaveX(ctx)
	a := seedAccount(t, r)
	b, err := r.CreateAccount(ctx, "second", "key-002", "secret", "", "zcard", "")
	if err != nil {
		t.Fatal(err)
	}
	d.Client.SupplierAccount.UpdateOneID(b.ID).SetOwnerUserID(u.ID).SetBalanceCache(99).ExecX(ctx)
	report, err := r.MigrateSharedWallet(ctx, map[uint64]uint64{a: u.ID}, true)
	if err != nil || report.Ready || report.Applied {
		t.Fatal(report, err)
	}
	if d.Client.SupplierAccount.GetX(ctx, a).SharedWallet {
		t.Fatal("partially migrated")
	}
	if d.Client.WalletTransaction.Query().CountX(ctx) != 0 {
		t.Fatal("partial credit")
	}
}

func TestNewSupplyAccountUsesExistingWallet(t *testing.T) {
	r, d := newSupplierTestData(t)
	ctx := context.Background()
	u := d.Client.User.Create().SetUsername("new-owner").SaveX(ctx)
	a, err := r.CreateApplication(ctx, u.ID, "zcard", "shop", "", "", "", "key-new", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if !a.SharedWallet || a.OwnerUserID != u.ID {
		t.Fatal(a)
	}
	if _, err := r.CreateApplication(ctx, 0, "zcard", "shop", "", "", "", "key-orphan", "secret"); err == nil {
		t.Fatal("ownerless creation accepted")
	}
	if err := r.Recharge(ctx, a.ID, 800, "initial-supply-credit", "test"); err != nil {
		t.Fatal(err)
	}
	view, err := NewStoreSupplierService(r, nil, nil).accountPB(ctx, a)
	if err != nil || view.BalanceCache != 800 || !view.SharedWallet {
		t.Fatal(view, err)
	}
}
