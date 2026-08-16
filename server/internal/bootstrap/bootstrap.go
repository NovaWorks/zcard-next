// Package bootstrap 模块装配（友商模式：模块互不感知，装配集中于此）。
//
// 跨模块绑定（通道 A：消费方接口 ← 被调方 port 实现）只发生在这里；
// 模块内部 wire 装配见各模块 providers.go。
package bootstrap

import (
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/authz"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/mods/dashboard"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/mods/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/procurement"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supplier"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"

	"github.com/google/wire"
)

// ProviderSet 全量业务装配（wire.Build 的聚合入口）。
// 里程碑推进时在此追加模块（procurement/supplier/...），模块间窄接口绑定也在此登记。
var ProviderSet = wire.NewSet(
	data.ProviderSet,
	securityProviderSet,
	queueProviderSet,
	idProviderSet, // 密钥解析（env 优先 → conf 兜底 → dev 随机告警）
	identity.ProviderSet,
	authz.ProviderSet,
	settings.ProviderSet,
	catalog.ProviderSet,
	inventory.ProviderSet,
	order.ProviderSet,
	payment.ProviderSet,
	wallet.ProviderSet,
	fulfillment.ProviderSet,
	memberlevel.ProviderSet,
	coupon.ProviderSet,
	dashboard.ProviderSet,
	supply.ProviderSet,
	procurement.ProviderSet,
	supplier.ProviderSet,
	// M1 预告：order ↔ payment 破环点绑定（payment.OrderLifecycle ← order 实现）
	// M3 预告：affiliate/reseller/ticket/notify/media/audit
)
