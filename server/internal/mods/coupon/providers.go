package coupon

// wire providers（）。
// 三端口绑定（order 管线消费，通道 A）：
// - CouponResolver（范围矩阵券）/ FlashResolver（秒杀同锁）/ PromotionResolver（促销最优）

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"

	"github.com/google/wire"
)

// ProviderSet coupon providers。
var ProviderSet = wire.NewSet(
	NewCouponRepoImpl,
	wire.Bind(new(port.CouponResolver), new(*CouponRepoImpl)),
	wire.Bind(new(port.FlashResolver), new(*CouponRepoImpl)),
	wire.Bind(new(port.PromotionResolver), new(*CouponRepoImpl)),
	NewAdminCouponService,
	NewStoreCouponService,
)
