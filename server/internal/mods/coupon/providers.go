package coupon

// wire providers。

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"

	"github.com/google/wire"
)

// ProviderSet coupon providers。
var ProviderSet = wire.NewSet(
	NewCouponRepoImpl,
	wire.Bind(new(port.CouponResolver), new(*CouponRepoImpl)),
	NewAdminCouponService,
)
