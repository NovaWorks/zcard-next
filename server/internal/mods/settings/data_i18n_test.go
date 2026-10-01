package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/currency"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/proto"
)

func baseCurrencyCode(ctx context.Context, d *data.Data) string {
	code, _ := data.BaseCurrency(ctx, d)
	return code
}

func i18nTestData(t *testing.T) (*data.Data, *AdminSettingsService, *AdminCurrencyService) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:i18n%d?mode=memory&cache=shared", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err = client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	if err = EnsureDefaultCurrencies(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d, NewAdminSettingsService(NewSettingsUsecase(NewRepoImpl(d))), NewAdminCurrencyService(d)
}

func TestI18nMergedLanguageValidation(t *testing.T) {
	ctx := context.Background()
	_, s, _ := i18nTestData(t)
	if _, err := s.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "i18n", Key: "default_locale", ValueJson: `"en"`}); errors.FromError(err).Reason != "settings.DEFAULT_LOCALE_DISABLED" {
		t.Fatalf("disabled default accepted: %v", err)
	}
	if _, err := s.UpdateSettings(ctx, &adminv1.UpdateSettingsRequest{Items: []*adminv1.SettingUpdate{
		{Group: "i18n", Key: "default_locale", ValueJson: `"en"`},
		{Group: "i18n", Key: "enabled_locales", ValueJson: `["en"]`},
	}}); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`[]`, `["en","en"]`, `["zh_CN"]`, `["en_US"]`, `null`} {
		if _, err := s.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "i18n", Key: "enabled_locales", ValueJson: raw}); err == nil {
			t.Fatalf("invalid enabled languages accepted: %s", raw)
		}
	}
}

func TestBaseCurrencySwitchRebasesAndLocksExistingLedger(t *testing.T) {
	ctx := context.Background()
	d, s, cs := i18nTestData(t)
	for _, c := range []struct{ code, rate string }{{"AUD", "0.2"}, {"USD", "0.14"}} {
		if _, err := cs.CreateCurrency(ctx, &adminv1.CreateCurrencyRequest{Code: c.code, Symbol: "$", Precision: 2, RateJson: c.rate}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "i18n", Key: "base_currency", ValueJson: `"AUD"`}); err != nil {
		t.Fatal(err)
	}
	for code, want := range map[string]float64{"CNY": 5, "AUD": 1, "USD": 0.7} {
		row := d.Client.Currency.Query().Where(currency.Code(code)).OnlyX(ctx)
		if row.Rate != want {
			t.Fatalf("%s rate=%v want %v", code, row.Rate, want)
		}
	}
	if baseCurrencyCode(ctx, d) != "AUD" {
		t.Fatal("base currency not saved")
	}
	if _, err := cs.UpdateCurrency(ctx, &adminv1.UpdateCurrencyRequest{Code: "AUD", RateJson: "0.5"}); err == nil {
		t.Fatal("base rate changed")
	}
	d.Client.WalletAccount.Create().SetUserID(7).SetCurrency("AUD").SaveX(ctx)
	if _, err := s.UpdateSettings(ctx, &adminv1.UpdateSettingsRequest{Items: []*adminv1.SettingUpdate{
		{Group: "site", Key: "name", ValueJson: `"must rollback"`},
		{Group: "i18n", Key: "base_currency", ValueJson: `"CNY"`},
	}}); errors.FromError(err).Reason != "settings.CURRENCY_CHANGE_LOCKED" {
		t.Fatalf("existing ledger accepted: %v", err)
	}
	if baseCurrencyCode(ctx, d) != "AUD" {
		t.Fatal("locked base changed")
	}
	if _, err := NewRepoImpl(d).Get(ctx, "site", "name"); err == nil {
		t.Fatal("rejected batch partly saved")
	}
	if d.Client.Currency.Query().Where(currency.Code("CNY")).OnlyX(ctx).Rate != 5 {
		t.Fatal("rejected switch changed rates")
	}
}

func TestCurrencyReferencesAndEnabledSelection(t *testing.T) {
	ctx := context.Background()
	_, s, cs := i18nTestData(t)
	if _, err := cs.CreateCurrency(ctx, &adminv1.CreateCurrencyRequest{Code: "AUD", Symbol: "A$", Precision: 2, RateJson: "0.2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.UpdateCurrency(ctx, &adminv1.UpdateCurrencyRequest{Code: "AUD", Enabled: proto.Bool(false)}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"base_currency", "display_currency"} {
		if _, err := s.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "i18n", Key: key, ValueJson: `"AUD"`}); errors.FromError(err).Reason != "settings.CURRENCY_NOT_ENABLED" {
			t.Fatalf("disabled selection accepted: %v", err)
		}
	}
	_, _ = cs.UpdateCurrency(ctx, &adminv1.UpdateCurrencyRequest{Code: "AUD", Enabled: proto.Bool(true)})
	if _, err := s.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "i18n", Key: "display_currency", ValueJson: `"AUD"`}); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"CNY", "AUD"} {
		if _, err := cs.UpdateCurrency(ctx, &adminv1.UpdateCurrencyRequest{Code: code, Enabled: proto.Bool(false)}); errors.FromError(err).Reason != "settings.CURRENCY_IN_USE" {
			t.Fatalf("referenced currency disabled: %v", err)
		}
		if _, err := cs.DeleteCurrency(ctx, &adminv1.DeleteCurrencyRequest{Code: code}); errors.FromError(err).Reason != "settings.CURRENCY_IN_USE" {
			t.Fatalf("referenced currency deleted: %v", err)
		}
	}
}

func TestBaseCurrencySwitchRejectsExistingPricing(t *testing.T) {
	ctx := context.Background()
	d, s, cs := i18nTestData(t)
	_, err := cs.CreateCurrency(ctx, &adminv1.CreateCurrencyRequest{Code: "AUD", Symbol: "$", Precision: 2, RateJson: "0.2"})
	if err != nil {
		t.Fatal(err)
	}
	d.Client.Product.Create().SetName("existing price").SetSlug("existing").SetPrice(100).SaveX(ctx)
	if _, err := s.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "i18n", Key: "base_currency", ValueJson: `"AUD"`}); errors.FromError(err).Reason != "settings.CURRENCY_CHANGE_LOCKED" {
		t.Fatalf("pricing relabeled: %v", err)
	}
}

func TestCurrencyRateStrictlyPositive(t *testing.T) {
	for _, raw := range []string{"0", "-1", "NaN", "Inf", "1e-99", "1000000000000", "not a number"} {
		if _, err := parseRate(raw); err == nil {
			t.Fatalf("bad rate accepted: %s", raw)
		}
	}
	if _, err := parseRate("0.2"); err != nil {
		t.Fatal(err)
	}
}

func TestUsecaseSingleLocaleWriteAlsoValidatesMergedState(t *testing.T) {
	r := &themeMemoryRepo{values: map[string]json.RawMessage{}}
	uc := NewSettingsUsecase(r)
	if err := uc.Put(context.Background(), "i18n", "default_locale", json.RawMessage(`"en"`)); err == nil {
		t.Fatal("single write bypassed locale validation")
	}
	if err := uc.PutMany(context.Background(), []port.Item{{Group: "i18n", Key: "default_locale", Value: json.RawMessage(`"zh_CN"`)}, {Group: "i18n", Key: "default_locale", Value: json.RawMessage(`"en"`)}}); err == nil {
		t.Fatal("duplicate setting accepted")
	}
}

func TestBaseCurrencySwitchProtectsCustomAmountSettings(t *testing.T) {
	ctx := context.Background()
	_, s, cs := i18nTestData(t)
	if _, err := cs.CreateCurrency(ctx, &adminv1.CreateCurrencyRequest{Code: "AUD", Symbol: "$", Precision: 2, RateJson: "0.2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "ticket", Key: "urgent_fee", ValueJson: "100"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "i18n", Key: "base_currency", ValueJson: `"AUD"`}); errors.FromError(err).Reason != "settings.CURRENCY_CHANGE_LOCKED" {
		t.Fatalf("custom amount relabeled: %v", err)
	}
	if _, err := s.UpdateSettings(ctx, &adminv1.UpdateSettingsRequest{Items: []*adminv1.SettingUpdate{
		{Group: "ticket", Key: "urgent_fee", ValueJson: "0"},
		{Group: "i18n", Key: "base_currency", ValueJson: `"AUD"`},
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestBaseCurrencySwitchRollsBackAllRates(t *testing.T) {
	ctx := context.Background()
	d, s, cs := i18nTestData(t)
	if _, err := cs.CreateCurrency(ctx, &adminv1.CreateCurrencyRequest{Code: "AUD", Symbol: "$", Precision: 2, RateJson: "0.2"}); err != nil {
		t.Fatal(err)
	}
	// Legacy disabled zero rates are still invalid during complete rebasing.
	d.Client.Currency.Create().SetCode("USD").SetSymbol("$").SetRate(0).SetEnabled(false).SaveX(ctx)
	if _, err := s.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "i18n", Key: "base_currency", ValueJson: `"AUD"`}); errors.FromError(err).Reason != "settings.CURRENCY_BAD_RATE" {
		t.Fatalf("invalid legacy rate accepted: %v", err)
	}
	for code, want := range map[string]float64{"CNY": 1, "AUD": 0.2} {
		if d.Client.Currency.Query().Where(currency.Code(code)).OnlyX(ctx).Rate != want {
			t.Fatalf("rejected switch partly rebased %s", code)
		}
	}
	if baseCurrencyCode(ctx, d) != "CNY" {
		t.Fatal("rejected switch changed base")
	}
}

func TestBaseCurrencySwitchProtectsIndependentAmountRules(t *testing.T) {
	for _, kind := range []string{"recharge_threshold", "spending_threshold", "spending_points", "fixed_payment_fee"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			d, settings, currencies := i18nTestData(t)
			if _, err := currencies.CreateCurrency(ctx, &adminv1.CreateCurrencyRequest{Code: "AUD", Symbol: "A$", Precision: 2, RateJson: "0.2"}); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "recharge_threshold":
				d.Client.MemberLevel.Create().SetName("VIP").SetThresholdRecharge(10000).SaveX(ctx)
			case "spending_threshold":
				d.Client.MemberLevel.Create().SetName("VIP").SetThresholdConsume(10000).SaveX(ctx)
			case "spending_points":
				d.Client.MemberLevel.Create().SetName("VIP").SetPointsRule(map[string]any{"spend_cents": 100, "points": 1}).SaveX(ctx)
			case "fixed_payment_fee":
				d.Client.PaymentChannel.Create().SetName("Card").SetCode("card").SetDriver("stripe").SetConfig([]byte("{}")).SetFeeType("fixed").SetFee(100).SaveX(ctx)
			}
			if _, err := settings.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "i18n", Key: "base_currency", ValueJson: `"AUD"`}); errors.FromError(err).Reason != "settings.CURRENCY_CHANGE_LOCKED" {
				t.Fatalf("amount rule relabeled as AUD: %v", err)
			}
			if baseCurrencyCode(ctx, d) != "CNY" || d.Client.Currency.Query().Where(currency.Code("AUD")).OnlyX(ctx).Rate != 0.2 {
				t.Fatal("rejected switch changed the currency or exchange rate")
			}
		})
	}
}

func TestBaseCurrencySwitchAllowsZeroAmountsAndPercentageRules(t *testing.T) {
	ctx := context.Background()
	d, settings, currencies := i18nTestData(t)
	if _, err := currencies.CreateCurrency(ctx, &adminv1.CreateCurrencyRequest{Code: "AUD", Symbol: "A$", Precision: 2, RateJson: "0.2"}); err != nil {
		t.Fatal(err)
	}
	d.Client.MemberLevel.Create().SetName("Basic").SetDiscount(9000).SetPointsRule(map[string]any{"spend_cents": 0, "points": 10}).SaveX(ctx)
	d.Client.PaymentChannel.Create().SetName("Free").SetCode("free").SetDriver("stripe").SetConfig([]byte("{}")).SetFeeType("fixed").SetFee(0).SaveX(ctx)
	d.Client.PaymentChannel.Create().SetName("Rate").SetCode("rate").SetDriver("stripe").SetConfig([]byte("{}")).SetFeeType("percent").SetFee(300).SaveX(ctx)
	if _, err := settings.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "i18n", Key: "base_currency", ValueJson: `"AUD"`}); err != nil {
		t.Fatalf("zero amounts or percentage rates prevented initial currency setup: %v", err)
	}
}
