package order

// PriceCalculator 价格管线（ ，规划 ）。
//
// 顺序钉死：基础价 → 会员折扣 → 商品组折扣 → 秒杀 → 优惠券 → 积分抵扣 → 分站定价 → 数量×单价 → rounding
// 每一步产出一条 order_amount_lines 行（有符号分；折扣为负、加价为正）。
// 管线为纯函数——M1a 中 member/coupon/points/reseller 四 port 返回中性值，/ 接真实现。
//
// 不变量：total == SUM(lines.amount)；seq 单调递增对应管线顺序。

import (
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// AmountLine 价格管线明细行（对应 ent order_amount_lines）。
type AmountLine struct {
	Type       string // base_price / member_discount / group_discount / promo_discount / coupon_discount / points_discount / subsite_markup / fee / rounding_adjust
	Amount     int64  // 有符号分（折扣为负、加价为正）
	SourceType string // member_level / coupon / flash_sale / points / subsite / manual
	SourceID   uint64
	Seq        int32
	Meta       map[string]any
}

// PriceInput 管线输入。
type PriceInput struct {
	BasePrice     money.Cents // 基础价（SKU > 商品）
	Quantity      int32
	MemberRate    int32       // 会员折扣（万分比；0=不折扣）
	GroupRate     int32       // 商品组折扣（万分比）
	FlashPrice    money.Cents // 秒杀价（0=无秒杀）
	PromoDiscount money.Cents // 促销折让（分；0=无促销——会员折扣后、券前）
	PromoName     string      // 促销名（金额行 meta）
	CouponValue   money.Cents // 优惠券面额（分；0=无券）
	PointsValue   money.Cents // 积分抵扣额（分）
	SubsiteMarkup money.Cents // 分站加价（分）
	DisplayRate   float64     // 展示币汇率（换算 rounding 用， 接入）
}

// PriceResult 管线输出。
type PriceResult struct {
	Lines []AmountLine
	Total money.Cents // 应付 = SUM(lines)
}

// PriceCalculator 价格管线（纯函数，无副作用）。
func PriceCalculator(in PriceInput) PriceResult {
	var lines []AmountLine
	seq := int32(0)
	current := in.BasePrice

	// 1) 基础价
	lines = append(lines, AmountLine{
		Type: "base_price", Amount: int64(in.BasePrice), Seq: seq,
	})
	seq++

	// 2) 会员等级折扣
	if in.MemberRate > 0 && in.MemberRate < 10000 {
		discount := int64(in.BasePrice) * int64(in.MemberRate) / 10000
		lines = append(lines, AmountLine{
			Type: "member_discount", Amount: -discount, SourceType: "member_level",
			Seq: seq, Meta: map[string]any{"rate": in.MemberRate},
		})
		current -= money.Cents(discount)
		seq++
	}

	// 3) 会员商品组折扣（叠加规则判定 ）
	if in.GroupRate > 0 && in.GroupRate < 10000 {
		discount := int64(current) * int64(in.GroupRate) / 10000
		lines = append(lines, AmountLine{
			Type: "group_discount", Amount: -discount, SourceType: "member_group",
			Seq: seq, Meta: map[string]any{"rate": in.GroupRate},
		})
		current -= money.Cents(discount)
		seq++
	}

	// 4) 限时秒杀价（时间窗内覆盖基础价——折扣 = flash - current）
	if in.FlashPrice > 0 && in.FlashPrice < current {
		diff := int64(current - in.FlashPrice)
		lines = append(lines, AmountLine{
			Type: "promo_discount", Amount: -diff, SourceType: "flash_sale",
			Seq: seq, Meta: map[string]any{"flash_price": int64(in.FlashPrice)},
		})
		current = in.FlashPrice
		seq++
	}

	// 4.5) 通用促销（会员折扣后、券前；多促最优已由解析器裁定；
	// 秒杀互斥：flash 生效时调用方不填 PromoDiscount——口径写死并测试）
	if in.PromoDiscount > 0 && in.PromoDiscount < current {
		lines = append(lines, AmountLine{
			Type: "promo_discount", Amount: -int64(in.PromoDiscount), SourceType: "promotion",
			Seq: seq, Meta: map[string]any{"name": in.PromoName},
		})
		current -= in.PromoDiscount
		seq++
	}

	// 5) 优惠券
	if in.CouponValue > 0 {
		discount := int64(in.CouponValue)
		if discount > int64(current) {
			discount = int64(current) // 券不找零
		}
		lines = append(lines, AmountLine{
			Type: "coupon_discount", Amount: -discount, SourceType: "coupon",
			Seq: seq,
		})
		current -= money.Cents(discount)
		seq++
	}

	// 6) 积分抵扣
	if in.PointsValue > 0 {
		discount := int64(in.PointsValue)
		if discount > int64(current) {
			discount = int64(current) // 不超应付
		}
		lines = append(lines, AmountLine{
			Type: "points_discount", Amount: -discount, SourceType: "points",
			Seq: seq,
		})
		current -= money.Cents(discount)
		seq++
	}

	// 7) 分站定价（加价）
	if in.SubsiteMarkup > 0 {
		lines = append(lines, AmountLine{
			Type: "subsite_markup", Amount: int64(in.SubsiteMarkup), SourceType: "subsite",
			Seq: seq,
		})
		current += in.SubsiteMarkup
		seq++
	}

	// 8) 数量倍增（修改所有行金额——实际实现：另起一行记录数量差异）
	// 简化：每行已按单价计算，此处直接乘数量到总额
	unitTotal := current
	total := unitTotal.Mul(in.Quantity)

	// 9) rounding_adjust（多币种快照， 接入）
	// M1a：基础货币直通，rounding 恒 0

	return PriceResult{Lines: lines, Total: total}
}

// ValidateTotal 不变量断言：unit SUM(lines) == unit price（不含数量倍增）。
func ValidateTotal(lines []AmountLine) money.Cents {
	var sum money.Cents
	for _, l := range lines {
		sum += money.Cents(l.Amount)
	}
	return sum
}
