package data

import "testing"

func TestSMSSalesEnabledByDefault(t *testing.T) {
	for _, value := range []string{"", "true", "false"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ZCARD_SMS_SALES_ENABLED", value)
			if got := SMSSalesEnabled(); got != (value != "false") {
				t.Fatalf("value=%q enabled=%v", value, got)
			}
		})
	}
}
