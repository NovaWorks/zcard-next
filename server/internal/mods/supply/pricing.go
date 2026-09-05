package supply

// 定价策略（）：本地价 = 上游价(分) × 汇率 × (1 + 加价%) → 取整模式。
// 纯函数，无 IO，供同步服务与单测直接调用。

import "math"

// 取整模式（对齐《数据库架构设计.md》 price_rounding_mode）。
const (
	RoundingNone      = "none"       // 不取整（四舍五入到分）
	RoundingCeilInt   = "ceil_int"   // 向上取整到整数元（分 → 整百）
	RoundingCeilTenth = "ceil_tenth" // 向上取整到 0.1 元（分 → 整十）
)

// ApplyPricing 按连接定价参数计算本地价（分）——组合加价：
// 本地价 = 上游价 × 汇率 × (1 + markupPercent%) + markupAmountCents → 取整。
// 固定额与百分比可同时配置（其一为 0 即单模式）；返回值恒 >= 0。
func ApplyPricing(upstreamCents int64, rate, markupPercent float64, markupAmountCents int64, mode string) int64 {
	if upstreamCents <= 0 {
		return 0
	}
	if rate <= 0 {
		rate = 1 // 未配置/非法汇率按 1（连接创建缺省值防御）
	}
	base := float64(upstreamCents) * rate
	raw := base*(1+markupPercent/100) + float64(markupAmountCents)
	result := roundByMode(raw, mode)
	// 低价商品百分比加价被分钱取整吃掉（如 2 分 ×10% = 2.2 → 2 = 成本价）：
	// 存在加价意图时至少 +1 分，保证渠道加价始终生效
	if (markupPercent > 0 || markupAmountCents > 0) && result <= roundByMode(base, mode) {
		result = roundByMode(base, mode) + 1
	}
	return result
}

// 导入定价模式（ D：交互式导入的策略选择；对齐 1.x computeInitialPrice）。
const (
	PriceModePercent = "percent" // 按连接加价 %（默认，同 ApplyPricing）
	PriceModeFixed   = "fixed"   // 上游价 + 固定金额（markup_amount 分）
	PriceModeEqual   = "equal"   // 原价（不加价）
	PriceModePending = "pending" // 待定价：不算价、导入后不上架（status=0）
)

// ApplyPricingImport 导入定价（先按模式算基数，再走连接取整）。
// markupAmountCents 仅 fixed 模式使用；pending 返回 0（调用方置 status=0 不上架）。
func ApplyPricingImport(upstreamCents int64, rate, markupPercent float64, markupAmountCents int64, mode, rounding string) int64 {
	if mode == PriceModePending {
		return 0
	}
	if rate <= 0 {
		rate = 1
	}
	base := float64(upstreamCents) * rate
	switch mode {
	case PriceModeFixed:
		raw := base + float64(markupAmountCents)
		return roundByMode(raw, rounding)
	case PriceModeEqual:
		return roundByMode(base, rounding)
	default: // percent
		return roundByMode(base*(1+markupPercent/100), rounding)
	}
}

func roundByMode(raw float64, mode string) int64 {
	switch mode {
	case RoundingCeilInt:
		return int64(math.Ceil(raw/100)) * 100
	case RoundingCeilTenth:
		return int64(math.Ceil(raw/10)) * 10
	default:
		return int64(math.Round(raw))
	}
}
