package money

import (
	"fmt"
	"github.com/shopspring/decimal"
	"math"
	"regexp"
)

var serviceRatePattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

// ExactServiceAmount keeps provider precision before FX and final-price rounding.
func ExactServiceAmount(rateText, mode string, quantity *int64, basis, runs int64) (string, error) {
	if len(rateText) > 128 || !serviceRatePattern.MatchString(rateText) {
		return "", fmt.Errorf("invalid service decimal rate")
	}
	rate, err := decimal.NewFromString(rateText)
	if err != nil || !rate.IsPositive() {
		return "", fmt.Errorf("invalid service decimal rate")
	}
	total := rate
	switch mode {
	case "quantity":
		if quantity == nil || *quantity <= 0 || basis <= 0 {
			return "", fmt.Errorf("service quantity billing is undefined")
		}
		var remainder decimal.Decimal
		total, remainder = total.Mul(decimal.NewFromInt(*quantity)).QuoRem(decimal.NewFromInt(basis), 256)
		if !remainder.IsZero() {
			return "", fmt.Errorf("service rate basis requires an explicit rounding rule")
		}
	case "fixed":
	case "per_event":
		return "", fmt.Errorf("subscription billing requires an explicit verified budget")
	default:
		return "", fmt.Errorf("service billing is undefined")
	}
	if runs < 1 {
		return "", fmt.Errorf("invalid service runs")
	}
	return total.Mul(decimal.NewFromInt(runs)).String(), nil
}

func ServiceAmountCents(exact string) (int64, error) {
	d, err := decimal.NewFromString(exact)
	if err != nil {
		return 0, fmt.Errorf("invalid service total amount")
	}
	d = d.Shift(2).Round(0)
	if !d.IsPositive() || d.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0, fmt.Errorf("invalid service total amount")
	}
	return d.IntPart(), nil
}
