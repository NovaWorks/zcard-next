package catalog

import (
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
)

// Service input and fulfillment are bound to its versioned upstream rules.
// Administrators may edit presentation/visibility; static card configuration
// cannot replace a paid or published service's routing and pricing semantics.
func validateSocialProduct(current *ent.Product, in *port.ProductInput) error {
	if current.DeliveryKind != "social_service" {

		return nil
	}
	if in.GoodsType != nil && *in.GoodsType != "virtual" || in.FulfillmentMode != "" && in.FulfillmentMode != "auto" || in.StockType != "" && in.StockType != "card" || in.DirectContent != nil || in.Price != 0 || in.FactoryPrice > 0 || in.PointsRequired != 0 || in.ManualStock != nil || in.PhysicalStock != nil {
		return fmt.Errorf("社交服务使用绑定货源的购买规则和动态报价，不能改为卡密、实物或人工交付")
	}
	return nil
}
