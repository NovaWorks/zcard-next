package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/currencyunit"

	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

type precisionCurrency struct {
	rate      string
	precision int32
}

func (c precisionCurrency) CurrencyByCode(context.Context, string) (string, int32, error) {
	return c.rate, c.precision, nil
}

func TestChargePrecisionIndependentOfDisplay(t *testing.T) {
	ctx := context.Background()
	for _, prec := range []int32{0, 1, 2, 3, 4, 8} {
		for _, tc := range []struct {
			driver, code, rate string
			amount             money.Cents
			units              int64
			precision          int32
		}{
			{"stripe", "USD", "0.14", 1000, 140, 2}, {"paypal", "USD", "0.14", 1000, 140, 2},
			{"stripe", "JPY", "20", 200, 40, 0}, {"paypal", "JPY", "20", 200, 40, 0},
			{"paypal", "HUF", "50", 200, 100, 0}, {"stripe", "HUF", "50", 200, 10000, 2},
			{"stripe", "ISK", "20.125", 200, 4000, 2}, {"stripe", "UGX", "500", 200, 100000, 2},
			{"stripe", "KWD", "0.05", 200, 100, 3}, {"epusdt", "USD", "0.14", 1000, 140, 2},
		} {
			t.Run(fmt.Sprintf("%s-%s-display%d", tc.driver, tc.code, prec), func(t *testing.T) {
				repo := &PaymentRepoImpl{currency: precisionCurrency{tc.rate, prec}}
				cfg := json.RawMessage(fmt.Sprintf(`{"target_currency":%q}`, tc.code))
				if tc.driver == "epusdt" {
					cfg = json.RawMessage(fmt.Sprintf(`{"currency":%q}`, tc.code))
				}
				snap, err := repo.computeCharge(ctx, tc.driver, cfg, tc.amount)
				if err != nil || snap.Units != tc.units || snap.Precision != tc.precision {
					t.Fatalf("snap=%+v err=%v", snap, err)
				}
			})
		}
	}
	repo := &PaymentRepoImpl{currency: precisionCurrency{"0.14", 4}}
	snap, err := repo.computeCharge(ctx, "epusdt", json.RawMessage(`{"currency":"usd"}`), 1000)
	if err != nil || snap.Units != 140 {
		t.Fatalf("GMPay currency: %+v %v", snap, err)
	}
	for _, cfg := range []string{`{}`, `{"target_currency":"CNY"}`} {
		snap, err := repo.computeCharge(ctx, "epay", json.RawMessage(cfg), 200)
		if err != nil || snap.Units != 0 {
			t.Fatalf("CNY changed: %+v %v", snap, err)
		}
	}
	for _, rate := range []string{"", "0", "-1", "NaN", "1e30", "0.000000001"} {
		repo.currency = precisionCurrency{rate, 4}
		if _, err := repo.computeCharge(ctx, "stripe", json.RawMessage(`{"target_currency":"USD"}`), 200); err == nil {
			t.Fatalf("accepted rate %q", rate)
		}
	}
	repo.currency = nil
	if _, err := repo.computeCharge(ctx, "stripe", json.RawMessage(`{"target_currency":"USD"}`), 200); err == nil {
		t.Fatal("missing currency must not fall back to CNY")
	}
	repo.currency = precisionCurrency{"0.00000001", 4}
	if _, err := repo.computeCharge(ctx, "stripe", json.RawMessage(`{"target_currency":"USD"}`), 1); err == nil {
		t.Fatal("zero converted amount accepted")
	}
	if _, err := repo.computeCharge(ctx, "epay", json.RawMessage(`{"target_currency":"USD"}`), 200); err == nil {
		t.Fatal("unsupported driver accepted")
	}
}

func TestCallbackPrecisionSnapshotAndLegacyReview(t *testing.T) {
	for _, tc := range []struct {
		name      string
		precision int32
		units     int64
		review    bool
	}{
		{"new_snapshot", 2, 140, false}, {"legacy_valid", -1, 140, false}, {"legacy_wrong_display_scale", -1, 14000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, repo, _, _, lifecycle, _ := newCallbackEnv(t)
			ctx := context.Background()
			// Changed live rate AND display precision must have no effect on old callbacks.
			repo.currency = precisionCurrency{"100", 4}
			o, p := seedPendingOrder(t, d, "epay", 1000)
			_, err := d.Client.Payment.UpdateOneID(p.ID).SetDriverSnapshot("stripe").SetChargedUnits(tc.units).SetChargedCurrency("USD").SetExchangeRate(0.14).SetChargedPrecision(tc.precision).Save(ctx)
			if err != nil {
				t.Fatal(err)
			}
			fact := CallbackFact{Channel: "epay", OrderNo: o.OrderNo, ChannelOrderNo: "precision-test", Currency: "USD", Amount: tc.units, Success: true}
			for i := 0; i < 2; i++ {
				if err := repo.HandleCallback(ctx, p.ID, fact); err != nil {
					t.Fatal(err)
				}
			}
			got, err := d.Client.Payment.Get(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.review {
				if got.ReviewReason == "" || len(lifecycle.markPaidCalls) != 0 || got.ChargedAmount != 0 {
					t.Fatalf("ambiguous payment fulfilled: %+v", got)
				}
			} else if got.ReviewReason != "" || got.ChargedAmount != 1000 || len(lifecycle.markPaidCalls) != 1 || got.ChargedPrecision != 2 {
				t.Fatalf("snapshot/idempotency failed: %+v calls=%v", got, lifecycle.markPaidCalls)
			}
		})
	}
}

func TestChargeSnapshotPersistsPrecision(t *testing.T) {
	d, repo, _, _, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	_, p := seedPendingOrder(t, d, "epay", 200)
	if err := repo.snapshotCharge(ctx, p.ID, ChargeSnapshot{Units: 40, Currency: "JPY", Rate: 20, Precision: 0}); err != nil {
		t.Fatal(err)
	}
	got, err := d.Client.Payment.Get(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ChargedPrecision != 0 || got.ChargedUnits != 40 || got.Amount != 200 {
		t.Fatalf("wrong snapshot: %+v", got)
	}
	unit, err := currencyunit.CurrencyChargeUnit("paypal", "JPY")
	if err != nil {
		t.Fatal(err)
	}
	if text := currencyunit.FormatChargeAmount(got.ChargedUnits, unit); text != "40" {
		t.Fatalf("JPY=%s", text)
	}
}

func TestLegacyPaypalZeroDecimalCallback(t *testing.T) {
	d, repo, _, _, lifecycle, _ := newCallbackEnv(t)
	ctx := context.Background()
	o, p := seedPendingOrder(t, d, "epay", 200)
	// Historical 2 CNY @20 = 40 JPY, stored as 4000 and sent as "40.00".
	_, err := d.Client.Payment.UpdateOneID(p.ID).SetDriverSnapshot("paypal").SetChargedUnits(4000).SetChargedCurrency("JPY").SetExchangeRate(20).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.HandleCallback(ctx, p.ID, CallbackFact{Channel: "epay", OrderNo: o.OrderNo, ChannelOrderNo: "legacy-jpy", Currency: "JPY", Amount: 40, Success: true}); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Client.Payment.Get(ctx, p.ID)
	if got.ChargedAmount != 200 || got.ReviewReason != "" || len(lifecycle.markPaidCalls) != 1 {
		t.Fatalf("legacy JPY: %+v", got)
	}
}
