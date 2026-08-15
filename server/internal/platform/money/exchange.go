package money

// 展示层与下单快照的跨币种换算（规划 §5.1 / §3.4）：
// 记账永远是基础货币 Cents（默认 CNY，最小单位=分，即 2 位小数）；
// 换算只发生在展示与快照，中间量用 decimal 后取整，
// 展示价与记账价的差由调用方记入 rounding_adjust（禁止 float 中转）。

import (
	"github.com/shopspring/decimal"
)

// DefaultDisplayPrecision CNY/USD 等常用货币的小数位。
const DefaultDisplayPrecision = 2

// baseScale 基础货币最小单位（分 = 10^-2 元）。
var baseScale = decimal.NewFromInt(100)

// Exchange 单笔换算结果（快照用）。
type Exchange struct {
	// Rate 换算汇率快照（1 基础货币 = Rate 目标币；与 currencies.rate 同义）
	Rate decimal.Decimal
	// DisplayAmount 目标币金额（以该币最小单位计的整数，如 USD cent）
	DisplayAmount int64
	// Rounded 往返取舍量（基础货币分；记入 order_amount_lines 的 rounding_adjust）
	Rounded Cents
}

// displayUnits 计算目标币最小单位数：amount(分) ÷ 100 × rate × 10^precision，四舍五入。
func displayUnits(amount int64, rate decimal.Decimal, precision int32) int64 {
	scale := decimal.New(1, precision)
	return decimal.NewFromInt(amount).
		Div(baseScale).
		Mul(rate).
		Mul(scale).
		Round(0).
		IntPart()
}

// baseFromUnits 反算基础货币分：units ÷ 10^precision ÷ rate × 100，四舍五入。
func baseFromUnits(units int64, rate decimal.Decimal, precision int32) int64 {
	scale := decimal.New(1, precision)
	return decimal.NewFromInt(units).
		Div(scale).
		Div(rate).
		Mul(baseScale).
		Round(0).
		IntPart()
}

// safeRate 汇率缺失/非法时 1:1 直通（宁可不错换，调用方以基础货币口径继续）。
func safeRate(rate decimal.Decimal) decimal.Decimal {
	if rate.IsZero() || rate.IsNegative() {
		return decimal.NewFromInt(1)
	}
	return rate
}

// ToDisplay 基础货币分 → 展示币金额：
// display = (amount ÷ 100) × rate，四舍五入到 precision 位小数（最小单位整数）；
// Rounded = 回算(round(display ÷ rate × 100)) − amount（对账容差，§5.1 回调核对 ± rounding）。
func ToDisplay(amount Cents, rate decimal.Decimal, precision int32) Exchange {
	if precision < 0 {
		precision = DefaultDisplayPrecision
	}
	rate = safeRate(rate)
	units := displayUnits(int64(amount), rate, precision)
	return Exchange{
		Rate:          rate,
		DisplayAmount: units,
		Rounded:       Cents(baseFromUnits(units, rate, precision)) - amount,
	}
}

// FromDisplay 展示币金额 → 基础货币分（下单快照反向）。
// 返回 (基础分, rounding)——rounding = 取整值 − 精确值（记 amount_lines）。
func FromDisplay(units int64, rate decimal.Decimal, precision int32) (Cents, Cents) {
	if precision < 0 {
		precision = DefaultDisplayPrecision
	}
	rate = safeRate(rate)
	base := baseFromUnits(units, rate, precision)
	exact := baseFromUnitsExact(units, rate, precision)
	return Cents(base), Cents(base) - Cents(exact)
}

func baseFromUnitsExact(units int64, rate decimal.Decimal, precision int32) int64 {
	scale := decimal.New(1, precision)
	return decimal.NewFromInt(units).
		Div(scale).
		Div(rate).
		Mul(baseScale).
		Truncate(0).
		IntPart()
}
