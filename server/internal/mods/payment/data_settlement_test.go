package payment

import (
	"context"
	"encoding/json"
	"testing"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/rechargeorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/walletaccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/wallettransaction"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
)

func setAUDBase(t *testing.T, d *data.Data) {
	t.Helper()
	d.Client.Setting.Create().SetGroup("i18n").SetKey("base_currency").SetValue(json.RawMessage(`"AUD"`)).SaveX(context.Background())
}

func TestAUDWalletPaymentAndRefundKeepCurrency(t *testing.T) {
	d, repo, ledger, _, _, _ := newCallbackEnv(t)
	setAUDBase(t, d)
	ctx := checkoutUser()
	if err := ledger.CreditInTx(ctx, wallet.Entry{UserID: 1, Direction: "in", Type: "adjust", Amount: 10000, Reference: "seed-aud"}); err != nil {
		t.Fatal(err)
	}
	o, _ := seedPendingOrder(t, d, "balance", 2500)
	d.Client.Order.UpdateOne(o).SetBaseCurrency("AUD").SaveX(ctx)
	svc := NewStorePaymentService(repo, d)
	channels, err := svc.ListChannels(ctx, &storefrontv1.ListPaymentChannelsRequest{})
	if err != nil || len(channels.Channels) != 1 || channels.Channels[0].Driver != "wallet" {
		t.Fatalf("CNY-only payment channel available for AUD: %+v %v", channels, err)
	}
	result, err := svc.CreatePayment(ctx, &storefrontv1.CreatePaymentRequest{OrderNo: o.OrderNo, Channel: "balance"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.HandleCallback(ctx, result.PaymentId, CallbackFact{Channel: "balance", Amount: 2500, Currency: "AUD", Success: true}); err != nil {
		t.Fatal(err)
	}
	acc := d.Client.WalletAccount.Query().Where(walletaccount.UserID(1)).OnlyX(ctx)
	if acc.Currency != "AUD" || acc.Available != 7500 {
		t.Fatalf("wallet payment or duplicate callback changed money: %+v", acc)
	}
	// The callback fixture records lifecycle calls; emulate its paid transition to
	// exercise the real wallet refund receipt and ledger, including duplicate guards.
	d.Client.Order.UpdateOne(o).SetStatus(order.StatusPaid).SaveX(ctx)
	zero := int64(0)
	if _, err := repo.RefundToWallet(ctx, o.ID, 2500, &zero, "AUD refund", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RefundToWallet(ctx, o.ID, 2500, &zero, "duplicate", 1); err == nil {
		t.Fatal("duplicate refund accepted")
	}
	acc = d.Client.WalletAccount.Query().Where(walletaccount.UserID(1)).OnlyX(ctx)
	if acc.Currency != "AUD" || acc.Available != 10000 {
		t.Fatalf("refund relabeled or changed money: %+v", acc)
	}
	rows := d.Client.WalletTransaction.Query().Where(wallettransaction.UserID(1)).AllX(ctx)
	for _, row := range rows {
		if row.Currency != "AUD" {
			t.Fatalf("ledger lost currency snapshot: %+v", row)
		}
	}
}

func TestAUDChargeUsesProviderSnapshotAndRejectsWrongCurrency(t *testing.T) {
	for _, driver := range []string{"stripe", "paypal"} {
		t.Run(driver, func(t *testing.T) {
			d, repo, _, _, lifecycle, _ := newCallbackEnv(t)
			setAUDBase(t, d)
			ctx := context.Background()
			snap, err := repo.computeCharge(ctx, driver, json.RawMessage(`{}`), 1234)
			if err != nil || snap.Currency != "AUD" || snap.Units != 1234 || snap.Rate != 1 || snap.Precision != 2 {
				t.Fatalf("same-currency AUD snapshot: %+v %v", snap, err)
			}
			o, p := seedPendingOrder(t, d, "epay", 1234)
			d.Client.Order.UpdateOne(o).SetBaseCurrency("AUD").SaveX(ctx)
			d.Client.Payment.UpdateOne(p).SetChargedUnits(snap.Units).SetChargedCurrency(snap.Currency).SetChargedPrecision(snap.Precision).SetExchangeRate(snap.Rate).SetDriverSnapshot(driver).SaveX(ctx)
			fact := CallbackFact{Channel: "epay", OrderNo: o.OrderNo, Amount: 1234, Currency: "CNY", Success: true}
			if err := repo.HandleCallback(ctx, p.ID, fact); err == nil {
				t.Fatal("CNY callback accepted for AUD payment")
			}
			fact.Currency = "AUD"
			if err := repo.HandleCallback(ctx, p.ID, fact); err != nil {
				t.Fatal(err)
			}
			got := d.Client.Payment.GetX(ctx, p.ID)
			if got.ChargedAmount != 1234 || len(lifecycle.markPaidCalls) != 1 {
				t.Fatalf("AUD confirmation used CNY: %+v", got)
			}
		})
	}
	for _, driver := range []string{"alipay", "wechat", "epay", "xunhupay", "bepusdt", "upay"} {
		if _, err := chargeCurrency(driver, json.RawMessage(`{}`), "AUD"); err == nil {
			t.Fatalf("CNY-only driver %s accepted AUD", driver)
		}
	}
}

func TestAUDWalletRejectsLegacyCNYOrder(t *testing.T) {
	d, _, ledger, _, _, _ := newCallbackEnv(t)
	setAUDBase(t, d)
	ctx := context.Background()
	o, _ := seedPendingOrder(t, d, "balance", 100)
	if err := ledger.CreditInTx(ctx, wallet.Entry{UserID: 1, Direction: "in", Type: "order_refund", Amount: 100, Reference: "wrong-currency", OrderID: o.ID}); err == nil {
		t.Fatal("CNY order credited an AUD wallet")
	}
	if d.Client.WalletAccount.Query().ExistX(ctx) || d.Client.WalletTransaction.Query().ExistX(ctx) {
		t.Fatal("rejected credit left money behind")
	}
}

func TestAUDDoesNotDisplayLegacyCNYWalletAsAUD(t *testing.T) {
	d, _, ledger, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	if err := ledger.CreditInTx(ctx, wallet.Entry{UserID: 1, Direction: "in", Type: "adjust", Amount: 100, Reference: "legacy-cny"}); err != nil {
		t.Fatal(err)
	}
	// Simulate a legacy setting saved before base currency affected accounting.
	d.Client.Setting.Update().Where(setting.Group("i18n"), setting.Key("base_currency")).SetValue(json.RawMessage(`"AUD"`)).SaveX(ctx)
	if _, _, err := ledger.GetBalance(ctx, 1); err == nil {
		t.Fatal("legacy CNY balance displayed as AUD")
	}
	if _, _, err := ledger.ListTransactions(ctx, 1, 1, 20); err == nil {
		t.Fatal("legacy CNY ledger displayed as AUD")
	}
}

func TestAUDRejectsLegacyCNYRefundWithoutMoneyMutation(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	setAUDBase(t, d)
	ctx := context.Background()
	o, p := seedPendingOrder(t, d, "epay", 100)
	d.Client.Order.UpdateOne(o).SetStatus(order.StatusPaid).SaveX(ctx)
	d.Client.Payment.UpdateOne(p).SetStatus(payment.StatusSuccess).SaveX(ctx)
	zero := int64(0)
	if _, err := repo.RefundToWallet(ctx, o.ID, 100, &zero, "legacy refund", 1); err == nil {
		t.Fatal("legacy CNY refund credited AUD wallet")
	}
	if d.Client.WalletAccount.Query().ExistX(ctx) || d.Client.WalletTransaction.Query().ExistX(ctx) || d.Client.RefundOrder.Query().ExistX(ctx) {
		t.Fatal("rejected refund left money or receipt behind")
	}
	if got := d.Client.Order.GetX(ctx, o.ID); got.Status != order.StatusPaid || got.Version != 0 {
		t.Fatalf("rejected refund changed order: %+v", got)
	}
}

func TestRechargeHonorsBaseCurrencySnapshot(t *testing.T) {
	for _, target := range []rechargeorder.Target{rechargeorder.TargetBalance, rechargeorder.TargetSupply} {
		t.Run(string(target), func(t *testing.T) {
			d, repo, _, _, _, supplier := newCallbackEnv(t)
			setAUDBase(t, d)
			ctx := context.Background()
			ro := d.Client.RechargeOrder.Create().SetUserID(1).SetAmount(100).SetGiftAmount(10).SetTarget(target).SetSupplierAccountID(77).SaveX(ctx)
			p := d.Client.Payment.Create().SetRechargeOrderID(ro.ID).SetChannel("epay").SetAmount(100).SetStatus(payment.StatusPending).SaveX(ctx)
			if err := repo.HandleCallback(ctx, p.ID, CallbackFact{Channel: "epay", Amount: 100, Currency: "CNY", Success: true}); err == nil {
				t.Fatal("legacy CNY recharge credited AUD account")
			}
			if d.Client.WalletAccount.Query().ExistX(ctx) || d.Client.WalletTransaction.Query().ExistX(ctx) {
				t.Fatal("rejected recharge left money behind")
			}
			if supplier.calls != 0 {
				t.Fatal("rejected recharge reached supplier balance")
			}
			if d.Client.RechargeOrder.GetX(ctx, ro.ID).Status != rechargeorder.StatusPending || d.Client.Payment.GetX(ctx, p.ID).Status != payment.StatusPending {
				t.Fatal("rejected recharge changed settlement state")
			}
			// New AUD attempts carry their source currency even when no conversion
			// is needed, and may settle without relabeling their principal or gift.
			d.Client.Payment.UpdateOne(p).SetPricingSnapshot(json.RawMessage(`{"base_currency":"AUD"}`)).SaveX(ctx)
			if err := repo.HandleCallback(ctx, p.ID, CallbackFact{Channel: "epay", Amount: 100, Currency: "AUD", Success: true}); err != nil {
				t.Fatal(err)
			}
			if target == rechargeorder.TargetBalance {
				acc := d.Client.WalletAccount.Query().Where(walletaccount.UserID(1)).OnlyX(ctx)
				if acc.Currency != "AUD" || acc.Available != 110 {
					t.Fatalf("AUD recharge money mismatch: %+v", acc)
				}
			} else if supplier.calls != 1 || supplier.amount != 110 || supplier.accountID != 77 {
				t.Fatalf("AUD supplier recharge money mismatch: %+v", supplier)
			}
		})
	}
}
