package supply

// 定价策略单测：取整三模式矩阵 + 价格保护语义（P2-01 必测项）。

import "testing"

func TestApplyPricing(t *testing.T) {
	cases := []struct {
		name    string
		up      int64 // 上游价（分）
		rate    float64
		markup  float64
		mode    string
		want    int64
	}{
		{"none_原始价", 1000, 1, 0, RoundingNone, 1000},
		{"none_汇率", 1000, 0.9, 0, RoundingNone, 900},
		{"none_加价10%", 1000, 1, 10, RoundingNone, 1100},
		{"none_汇率加价组合", 1234, 1.5, 20, RoundingNone, 2221}, // 1234*1.5*1.2=2221.2 → round 2221
		{"none_四舍五入", 1, 1, 0, RoundingNone, 1},
		{"none_精确到分", 105, 1.0/3, 0, RoundingNone, 35}, // 35.0
		{"ceil_int_整元", 1001, 1, 0, RoundingCeilInt, 1100},
		{"ceil_int_整元边界", 1000, 1, 0, RoundingCeilInt, 1000},
		{"ceil_int_加价后进位", 999, 1, 1, RoundingCeilInt, 1100}, // 999*1.01=1008.99 → 1100
		{"ceil_tenth_0.1元", 1001, 1, 0, RoundingCeilTenth, 1010},
		{"ceil_tenth_边界", 1000, 1, 0, RoundingCeilTenth, 1000},
		{"ceil_tenth_组合", 1234, 1.5, 20, RoundingCeilTenth, 2230}, // 2221.2 → 2230
		{"未知模式按none", 1001, 1, 0, "bogus", 1001},
		{"上游价0", 0, 1, 0, RoundingNone, 0},
		{"上游价负数", -5, 1, 0, RoundingNone, 0},
	}
	for _, c := range cases {
		if got := ApplyPricing(c.up, c.rate, c.markup, c.mode); got != c.want {
			t.Errorf("%s: ApplyPricing(%d, %v, %v, %q) = %d, want %d",
				c.name, c.up, c.rate, c.markup, c.mode, got, c.want)
		}
	}
}
