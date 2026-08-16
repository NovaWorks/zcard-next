// Package port 为 coupon 模块对外契约（零依赖包）。
package port

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// CouponResolver 优惠券解析（order 价格管线步骤 5 消费）。
type CouponResolver interface {
	// Resolve 校验券（存在/unused/未过期/用户匹配）并返回面额（分；percent 按 orderAmount 折算）。
	Resolve(ctx context.Context, code string, userID uint64, orderAmount money.Cents) (value money.Cents, couponID uint64, err error)
	// MarkUsed 核销券（下单事务内回填 used_order_id/used_at）。
	MarkUsed(ctx context.Context, couponID, orderID uint64) error
}
