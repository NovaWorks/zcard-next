// Package port 为 reseller 模块对外契约（零依赖包）。
package port

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// Pricer 分站定价窄接口（order 价格管线步骤 7 消费，通道 A）。
// listing 与 checkout 共用同一 ResolveUnitPrice（1.x 铁律——分站价只在一处计算）；
// ProfitEligible 为下单时的防自购快照判定（落 orders.profit_eligible）。
type Pricer interface {
	// ResolveUnitPrice 分站单价：SKU 规则 > 商品规则 > 分站默认加价率 > 继承主站价；
	// 4 模式换算 + 下限保护（不得低于主站基础价）。
	ResolveUnitPrice(ctx context.Context, subsiteID, productID, skuID uint64, basePrice money.Cents) (money.Cents, error)
	// ProfitEligible 防自购三查：买家==分站主 / 买家∈分站主上级链 / 反向命中 → false。
	ProfitEligible(ctx context.Context, subsiteID, buyerID uint64) bool
}
