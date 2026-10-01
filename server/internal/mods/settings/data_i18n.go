package settings

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"regexp"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/currency"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/paymentchannel"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"github.com/go-kratos/kratos/v3/errors"
	"github.com/shopspring/decimal"
)

var currencyCodePattern = regexp.MustCompile(`^[A-Z]{3}$`)

func supportedLocale(locale string) bool { return locale == "zh_CN" || locale == "en" }

func validateI18nValue(key string, value json.RawMessage) error {
	switch key {
	case "default_locale":
		var locale string
		if json.Unmarshal(value, &locale) != nil || !supportedLocale(locale) {
			return errors.BadRequest("settings.INVALID_LOCALE", "请选择简体中文或英语")
		}
	case "enabled_locales":
		var locales []string
		if json.Unmarshal(value, &locales) != nil || len(locales) == 0 || len(locales) > 2 {
			return errors.BadRequest("settings.INVALID_LOCALE", "至少启用一种语言，目前支持简体中文和英语")
		}
		seen := map[string]bool{}
		for _, locale := range locales {
			if !supportedLocale(locale) || seen[locale] {
				return errors.BadRequest("settings.INVALID_LOCALE", "启用语言须为不重复的简体中文或英语")
			}
			seen[locale] = true
		}
	case "base_currency", "display_currency":
		var code string
		if json.Unmarshal(value, &code) != nil || (!currencyCodePattern.MatchString(code) && !(key == "display_currency" && code == "")) {
			return errors.BadRequest("settings.INVALID_CURRENCY", "请选择有效的三位大写货币代码")
		}
	}
	return nil
}

func hasI18nWrites(items []port.Item) bool {
	for _, it := range items {
		if it.Group == "i18n" {
			return true
		}
	}
	return false
}

// Validate the resulting group, allowing default/enabled locales to change in one save.
func mergedI18n(current, changes []port.Item) (map[string]json.RawMessage, error) {
	g, _ := Group("i18n")
	values := map[string]json.RawMessage{}
	for key := range g.Defaults {
		v, _ := g.DefaultJSON(key)
		values[key] = json.RawMessage(v)
	}
	for _, list := range [][]port.Item{current, changes} {
		for _, it := range list {
			if it.Group == "i18n" {
				values[it.Key] = it.Value
			}
		}
	}
	for key, value := range values {
		if err := validateI18nValue(key, value); err != nil {
			return nil, err
		}
	}
	var defaultLocale string
	var enabled []string
	_ = json.Unmarshal(values["default_locale"], &defaultLocale)
	_ = json.Unmarshal(values["enabled_locales"], &enabled)
	for _, locale := range enabled {
		if locale == defaultLocale {
			return values, nil
		}
	}
	return nil, errors.BadRequest("settings.DEFAULT_LOCALE_DISABLED", "默认语言必须包含在启用语言列表中")
}

// These values have an implicit currency unit even before any orders exist.
var monetarySettingKeys = map[string][]string{
	"ticket":            {"urgent_fee"},
	"recharge":          {"min_amount", "max_amount", "gift_tiers"},
	"supplier_recharge": {"min_amount", "max_amount", "gift_tiers"},
	"withdraw":          {"min_amount", "fee_value"},
	"points":            {"deduct_rate"},
}

func hasCurrencyConfigurationWrites(items []port.Item) bool {
	if hasI18nWrites(items) {
		return true
	}
	for _, it := range items {
		for _, key := range monetarySettingKeys[it.Group] {
			if it.Key == key {
				return true
			}
		}
	}
	return false
}

func (r *RepoImpl) ensureDefaultMonetarySettings(ctx context.Context, changes []port.Item) error {
	current, err := r.List(ctx, "")
	if err != nil {
		return err
	}
	values := map[string]json.RawMessage{}
	for _, list := range [][]port.Item{current, changes} {
		for _, it := range list {
			values[it.Group+"."+it.Key] = it.Value
		}
	}
	for group, keys := range monetarySettingKeys {
		g, _ := Group(group)
		for _, key := range keys {
			raw, exists := values[group+"."+key]
			if !exists {
				continue
			}
			def, _ := g.DefaultJSON(key)
			var actual, expected any
			if json.Unmarshal(raw, &actual) != nil || json.Unmarshal([]byte(def), &expected) != nil || !reflect.DeepEqual(actual, expected) {
				return errors.Conflict("settings.CURRENCY_CHANGE_LOCKED", "已有自定义金额设置（"+g.Desc+"），请先恢复默认值后再切换基础货币，避免金额含义改变")
			}
		}
	}
	return nil
}

func (r *RepoImpl) prepareI18nWrites(ctx context.Context, items []port.Item) error {
	current, err := r.List(ctx, "i18n")
	if err != nil {
		return err
	}
	values, err := mergedI18n(current, items)
	if err != nil {
		return err
	}
	client := data.Client(ctx, r.data)
	var nextBase, display string
	_ = json.Unmarshal(values["base_currency"], &nextBase)
	_ = json.Unmarshal(values["display_currency"], &display)
	for _, code := range []string{nextBase, display} {
		if code == "" {
			continue
		}
		row, e := client.Currency.Query().Where(currency.Code(code), currency.Enabled(true)).Only(ctx)
		if ent.IsNotFound(e) {
			return errors.BadRequest("settings.CURRENCY_NOT_ENABLED", "货币不存在或未启用，请先在「货币」页创建并启用")
		}
		if e != nil {
			return e
		}
		if row.Rate <= 0 || math.IsNaN(row.Rate) || math.IsInf(row.Rate, 0) {
			return errors.BadRequest("settings.CURRENCY_BAD_RATE", "所选货币须设置大于零的有效汇率")
		}
	}
	base, err := data.BaseCurrency(ctx, r.data)
	if err != nil {
		return err
	}
	if nextBase == base {
		return nil
	}
	if err := r.ensureDefaultMonetarySettings(ctx, items); err != nil {
		return err
	}
	if err := ensureCurrencySwitchEmpty(ctx, client); err != nil {
		return err
	}
	rows, err := client.Currency.Query().All(ctx)
	if err != nil {
		return err
	}
	var divisor decimal.Decimal
	for _, row := range rows {
		if row.Code == nextBase {
			divisor = decimal.NewFromFloat(row.Rate)
		}
	}
	for _, row := range rows {
		if row.Rate <= 0 || math.IsNaN(row.Rate) || math.IsInf(row.Rate, 0) {
			return errors.BadRequest("settings.CURRENCY_BAD_RATE", "请先修复货币表中无效或为零的汇率")
		}
		rate := decimal.NewFromFloat(row.Rate).Div(divisor).Round(8)
		if row.Code == nextBase {
			rate = decimal.NewFromInt(1)
		}
		if !rate.IsPositive() || rate.GreaterThan(decimal.RequireFromString("999999999999.99999999")) {
			return errors.BadRequest("settings.CURRENCY_BAD_RATE", "切换后的汇率超出支持范围，请调整货币汇率")
		}
		f, _ := rate.Float64()
		if err := client.Currency.UpdateOneID(row.ID).SetRate(f).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// A single-currency ledger may only choose its unit before pricing/funding starts.
// Existing zero-balance accounts still prevent relabeling historical transactions.
func ensureCurrencySwitchEmpty(ctx context.Context, c *ent.Client) error {
	checks := []struct {
		name   string
		exists func(context.Context) (bool, error)
	}{
		{"会员金额阈值", c.MemberLevel.Query().Where(memberlevel.Or(memberlevel.ThresholdRechargeGT(0), memberlevel.ThresholdConsumeGT(0))).Exist},
		{"会员消费积分规则", func(ctx context.Context) (bool, error) {
			levels, err := c.MemberLevel.Query().All(ctx)
			if err != nil {
				return false, err
			}
			for _, level := range levels {
				// Consumption awards use integer cents and points; fractional
				// values below one truncate to zero and do not price spending.
				spend, _ := level.PointsRule["spend_cents"].(float64)
				points, _ := level.PointsRule["points"].(float64)
				if spend >= 1 && points >= 1 {
					return true, nil
				}
			}
			return false, nil
		}},
		{"固定支付手续费", c.PaymentChannel.Query().Where(paymentchannel.FeeTypeEQ(paymentchannel.FeeTypeFixed), paymentchannel.FeeGT(0)).Exist},
		{"商品", c.Product.Query().Exist},
		{"订单", c.Order.Query().Exist},
		{"支付记录", c.Payment.Query().Exist},
		{"钱包账户", c.WalletAccount.Query().Exist},
		{"钱包流水", c.WalletTransaction.Query().Exist},
		{"充值单", c.RechargeOrder.Query().Exist},
		{"提现单", c.Withdrawal.Query().Exist},
		{"退款单", c.RefundOrder.Query().Exist},
		{"优惠券", c.Coupon.Query().Exist},
		{"促销", c.Promotion.Query().Exist},
		{"秒杀", c.FlashSale.Query().Exist},
		{"礼品卡", c.Giftcard.Query().Exist},
		{"礼品卡批次", c.GiftcardBatch.Query().Exist},
		{"佣金", c.AffiliateCommission.Query().Exist},
		{"分站定价", c.ResellerPricing.Query().Exist},
		{"分站账户", c.ResellerBalanceAccount.Query().Exist},
		{"分站流水", c.ResellerLedgerEntry.Query().Exist},
		{"供货账户", c.SupplierAccount.Query().Exist},
		{"供货流水", c.SupplierLedgerEntry.Query().Exist},
		{"供货订单", c.SupplyOrder.Query().Exist},
		{"供货定价", c.SupplierProductPrice.Query().Exist},
		{"货源连接", c.SupplyConnection.Query().Exist},
		{"抽奖", c.LotteryActivity.Query().Exist},
	}
	for _, check := range checks {
		exists, err := check.exists(ctx)
		if err != nil {
			return err
		}
		if exists {
			return errors.Conflict("settings.CURRENCY_CHANGE_LOCKED", "已有"+check.name+"，不能直接切换基础货币；现有价格和资金需要专门迁移")
		}
	}
	return nil
}

func currencyIsReferenced(ctx context.Context, d *data.Data, code string) (bool, error) {
	base, err := data.BaseCurrency(ctx, d)
	if err != nil {
		return false, err
	}
	if code == base {
		return true, nil
	}
	raw, err := Get(ctx, d, "i18n", "display_currency")
	if err != nil && err.Error() != "settings.NOT_FOUND" {
		return false, err
	}
	var display string
	_ = json.Unmarshal(raw, &display)
	return display == code, nil
}
