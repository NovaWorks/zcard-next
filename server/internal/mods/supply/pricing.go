package supply

// 定价策略（P2-01 T3）：本地价 = 上游价(分) × 汇率 × (1 + 加价%) → 取整模式。
// 纯函数，无 IO，供同步服务与单测直接调用。

import "math"

// 取整模式（对齐《数据库架构设计.md》§4.7 price_rounding_mode）。
const (
	RoundingNone      = "none"       // 不取整（四舍五入到分）
	RoundingCeilInt   = "ceil_int"   // 向上取整到整数元（分 → 整百）
	RoundingCeilTenth = "ceil_tenth" // 向上取整到 0.1 元（分 → 整十）
)

// ApplyPricing 按连接定价三参数计算本地价（分）。
// upstreamCents 为上游价（分）；rate 汇率（默认 1）；markupPercent 加价百分比
// （100 = 翻倍）；mode 取整模式。返回值恒 >= 0。
func ApplyPricing(upstreamCents int64, rate, markupPercent float64, mode string) int64 {
	if upstreamCents <= 0 {
		return 0
	}
	raw := float64(upstreamCents) * rate * (1 + markupPercent/100)
	switch mode {
	case RoundingCeilInt:
		return int64(math.Ceil(raw/100)) * 100
	case RoundingCeilTenth:
		return int64(math.Ceil(raw/10)) * 10
	default: // none（含未知模式按 none 兜底）
		return int64(math.Round(raw))
	}
}
