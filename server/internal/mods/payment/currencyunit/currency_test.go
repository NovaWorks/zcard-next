package currencyunit

import "testing"

func TestProviderCurrencyUnits(t *testing.T) {
	for _, tc := range []struct {
		driver, code, value string
		units               int64
	}{
		{"paypal", "USD", "2.00", 200}, {"paypal", "JPY", "200", 200},
		{"paypal", "HUF", "200", 200}, {"paypal", "TWD", "200", 200},
		{"stripe", "JPY", "200", 200}, {"stripe", "USD", "2.00", 200},
	} {
		unit, err := CurrencyChargeUnit(tc.driver, tc.code)
		if err != nil {
			t.Fatal(err)
		}
		if got := FormatChargeAmount(tc.units, unit); got != tc.value {
			t.Fatalf("%s %s: %s", tc.driver, tc.code, got)
		}
		if got, err := ParseChargeAmount(tc.value, unit); err != nil || got != tc.units {
			t.Fatalf("parse %s: %d %v", tc.code, got, err)
		}
	}
	unit, _ := CurrencyChargeUnit("paypal", "JPY")
	for _, v := range []string{"1.1", "NaN", "-1", "9223372036854775808", "1e99"} {
		if _, err := ParseChargeAmount(v, unit); err == nil {
			t.Fatalf("accepted %s", v)
		}
	}
	for _, tc := range [][2]string{{"paypal", "KWD"}, {"stripe", "FAKE"}, {"epusdt", "JPY"}, {"epay", "USD"}} {
		if _, err := CurrencyChargeUnit(tc[0], tc[1]); err == nil {
			t.Fatalf("accepted %v", tc)
		}
	}
}
