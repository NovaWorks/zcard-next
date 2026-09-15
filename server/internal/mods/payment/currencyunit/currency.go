package currencyunit

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/shopspring/decimal"
	"golang.org/x/text/currency"
)

// ChargeUnit is a provider protocol unit, independent of currencies.precision
// (which only controls display). Step is the smallest accepted integer amount.
// Sources: https://docs.stripe.com/currencies
// https://developer.paypal.com/reference/currency-codes/
type ChargeUnit struct {
	Precision int32
	Step      int64
}

func CurrencyChargeUnit(driver, code string) (ChargeUnit, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	unit := ChargeUnit{Precision: 2, Step: 1}
	switch driver {
	case "paypal":
		if !strings.Contains(" AUD BRL CAD CNY CZK DKK EUR HKD HUF ILS JPY MYR MXN TWD NZD NOK PHP PLN GBP SGD SEK CHF THB USD ", " "+code+" ") {
			break
		}
		if code == "JPY" || code == "HUF" || code == "TWD" {
			unit.Precision = 0
		}
		return unit, nil
	case "stripe":
		iso, err := currency.ParseISO(code)
		if err != nil || len(code) != 3 || strings.HasPrefix(code, "X") && code != "XAF" && code != "XOF" && code != "XPF" {
			break
		}
		digits, _ := currency.Standard.Rounding(iso)
		unit.Precision = int32(digits)
		switch code {
		case "MGA":
			unit.Precision = 0
		case "ISK", "UGX":
			unit.Precision, unit.Step = 2, 100
		}
		if unit.Precision == 3 {
			unit.Step = 10
		}
		if unit.Precision <= 3 {
			return unit, nil
		}
	case "epusdt":
		// GMPay amount is denominated in fiat CNY/USD, not token atomic units.
		if code == "CNY" || code == "USD" {
			return unit, nil
		}
	}
	return ChargeUnit{}, fmt.Errorf("payment.CURRENCY_UNSUPPORTED: %s 不支持币种 %s", driver, code)
}

// ParseChargeAmount parses the provider's decimal string without rounding or
// overflow. Invalid precision cannot become a successful zero-value callback.
var chargeAmountPattern = regexp.MustCompile(`^\d+(?:\.\d+)?$`)

func ParseChargeAmount(value string, unit ChargeUnit) (int64, error) {
	if len(value) > 64 || !chargeAmountPattern.MatchString(value) {
		return 0, fmt.Errorf("payment.INVALID_CHARGE_AMOUNT")
	}
	d, err := decimal.NewFromString(value)
	if err != nil || len(value) > 64 || d.IsNegative() || unit.Precision < 0 || unit.Precision > 3 {
		return 0, fmt.Errorf("payment.INVALID_CHARGE_AMOUNT")
	}
	d = d.Shift(unit.Precision)
	if !d.Equal(d.Truncate(0)) || d.GreaterThan(decimal.NewFromInt(1<<63-1)) {
		return 0, fmt.Errorf("payment.INVALID_CHARGE_AMOUNT")
	}
	return d.IntPart(), nil
}

func FormatChargeAmount(units int64, unit ChargeUnit) string {
	return decimal.NewFromInt(units).Shift(-unit.Precision).StringFixed(unit.Precision)
}
