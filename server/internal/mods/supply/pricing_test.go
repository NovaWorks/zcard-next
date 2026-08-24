package supply

// 定价策略单测：取整三模式矩阵 + 价格保护语义（P2-01 必测项）。

import "testing"

func TestApplyPricing(t *testing.T) {
	cases := []struct {
		name   string
		up     int64 // 上游价（分）
		rate   float64
		markup float64
		amount int64 // 固定加价（分）
		mode   string
		want   int64
	}{
		{"none_原始价", 1000, 1, 0, 0, RoundingNone, 1000},
		{"none_汇率", 1000, 0.9, 0, 0, RoundingNone, 900},
		{"none_加价10%", 1000, 1, 10, 0, RoundingNone, 1100},
		{"none_汇率加价组合", 1234, 1.5, 20, 0, RoundingNone, 2221}, // 1234*1.5*1.2=2221.2 → round 2221
		{"none_固定加价", 1000, 1, 0, 500, RoundingNone, 1500},
		{"none_百分比+固定组合", 1000, 1, 10, 500, RoundingNone, 1600}, // 1100+500
		{"none_固定加价汇率后", 1000, 0.5, 0, 300, RoundingNone, 800}, // 500+300
		{"ceil_int_固定加价进位", 1001, 1, 0, 100, RoundingCeilInt, 1200}, // 1101 → 1200
		{"none_四舍五入", 1, 1, 0, 0, RoundingNone, 1},
		{"none_低价百分比加价至少1分", 2, 1, 10, 0, RoundingNone, 3}, // 2.2 → round 2 会被加价吃掉 → 至少 3
		{"none_低价固定加价至少1分", 2, 1, 0, 1, RoundingNone, 3},   // 3 本就生效
		{"none_汇率低价加价至少1分", 1, 1, 50, 0, RoundingNone, 2},  // 1.5 → 1 被吃 → 2
		{"none_精确到分", 105, 1.0 / 3, 0, 0, RoundingNone, 35}, // 35.0
		{"ceil_int_整元", 1001, 1, 0, 0, RoundingCeilInt, 1100},
		{"ceil_int_整元边界", 1000, 1, 0, 0, RoundingCeilInt, 1000},
		{"ceil_int_加价后进位", 999, 1, 1, 0, RoundingCeilInt, 1100}, // 999*1.01=1008.99 → 1100
		{"ceil_tenth_0.1元", 1001, 1, 0, 0, RoundingCeilTenth, 1010},
		{"ceil_tenth_边界", 1000, 1, 0, 0, RoundingCeilTenth, 1000},
		{"ceil_tenth_组合", 1234, 1.5, 20, 0, RoundingCeilTenth, 2230}, // 2221.2 → 2230
		{"未知模式按none", 1001, 1, 0, 0, "bogus", 1001},
		{"上游价0", 0, 1, 0, 0, RoundingNone, 0},
		{"上游价负数", -5, 1, 0, 0, RoundingNone, 0},
	}
	for _, c := range cases {
		if got := ApplyPricing(c.up, c.rate, c.markup, c.amount, c.mode); got != c.want {
			t.Errorf("%s: ApplyPricing(%d, %v, %v, %d, %q) = %d, want %d",
				c.name, c.up, c.rate, c.markup, c.amount, c.mode, got, c.want)
		}
	}
}
