// Package port 为 coupon 模块对外契约（零依赖包）。
package port

import (
	"context"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// CartItem 管线购物车行（券范围判定输入）。
type CartItem struct {
	ProductID  uint64
	CategoryID uint64
	Quantity   int32
	UnitPrice  money.Cents
}

// CouponResolver 优惠券解析（order 价格管线步骤 5 消费）。
type CouponResolver interface {
	// ResolveScoped 校验券（存在/unused/未过期/用户匹配/范围矩阵/每人限用）
	// 并返回面额（分；percent 按订单应付折算，不找零）。
	ResolveScoped(ctx context.Context, code string, userID, levelID uint64, items []CartItem) (value money.Cents, couponID uint64, err error)
	// MarkUsed 核销券（下单事务内回填 used_order_id/used_at）。
	MarkUsed(ctx context.Context, couponID, orderID uint64) error
	// ReturnByOrder 返还券（取消/退款；过期不返——行恢复 unused 并清订单回填）。
	ReturnByOrder(ctx context.Context, orderID uint64) error
}

// FlashInfo 生效秒杀（管线步骤 4 输入）。
type FlashInfo struct {
	ID            uint64
	FlashPrice    money.Cents
	StartAt       time.Time // 限购累计窗口起点
	PerUserLimit  int32
}

// FlashResolver 秒杀解析（order 价格管线步骤 4 + 同锁扣减消费）。
type FlashResolver interface {
	// Active 生效中秒杀（窗口判定无状态；无则返回 nil）。
	Active(ctx context.Context, productID, skuID uint64) (*FlashInfo, error)
	// Consume 同锁扣减（inventory.Reserve 成功后、同一事务内调用；
	// CAS：sold_qty+qty<=limit_qty，affected==0 → ErrFlashSoldOut）。
	Consume(ctx context.Context, flashID uint64, qty int32) error
	// UserPurchasedCount 用户在秒杀窗口内已购数量（paid+pending 累计，限购判据）。
	UserPurchasedCount(ctx context.Context, productID, userID uint64, since time.Time) (int32, error)
}

// ErrFlashSoldOut 秒杀限量不足（哨兵：事务内回滚）。
var ErrFlashSoldOut = errNew("coupon: 秒杀已抢完")

// ErrFlashUserLimit 秒杀限购（哨兵）。
var ErrFlashUserLimit = errNew("coupon: 秒杀限购")

// PromotionInfo 生效促销（管线：会员折扣后、券前；多促取最优）。
type PromotionInfo struct {
	ID           uint64
	Type         string // fixed | percent | special_price
	Threshold    money.Cents
	Discount     money.Cents // fixed 面额（分）
	DiscountRate int32       // percent 万分比
	SpecialPrice money.Cents
	Name         string
}

// PromotionResolver 促销解析。
type PromotionResolver interface {
	// BestFor 取商品命中的最优促销（同时窗多促取折让最大；无则 nil）。
	BestFor(ctx context.Context, productID, categoryID uint64, unitPrice money.Cents) (*PromotionInfo, error)
}

// DiscountFor 促销折让（fixed=面额；percent=价×万分比；special_price=价-特价）。
// 纯函数——管线与测试共用同一口径。
func (p *PromotionInfo) DiscountFor(unitPrice money.Cents) money.Cents {
	switch p.Type {
	case "fixed":
		return p.Discount
	case "percent":
		return money.Cents(int64(unitPrice) * int64(p.DiscountRate) / 10000)
	case "special_price":
		if p.SpecialPrice < unitPrice {
			return unitPrice - p.SpecialPrice
		}
	}
	return 0
}

func errNew(s string) error { return &sentinel{s: s} }

type sentinel struct{ s string }

func (e *sentinel) Error() string { return e.s }
