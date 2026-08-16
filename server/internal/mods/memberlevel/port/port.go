// Package port 为 memberlevel 模块对外契约（零依赖包）。
package port

import "context"

// RateResolver 会员等级折扣解析（order 价格管线步骤 2 消费）。
// 返回万分比折扣（0=不折扣）+ 命中的等级 ID。无等级/用户不存在返回 0。
type RateResolver interface {
	EffectiveRate(ctx context.Context, userID uint64) (rate int32, levelID uint64, err error)
}
