package catalog

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/NovaWorks/zcard-next/server/internal/platform/shipping"
)

func (r *ProductRepoImpl) validatePhysicalProduct(ctx context.Context, p *ent.Product, in *port.ProductInput) error {
	kind := "virtual"
	if p != nil {
		kind = p.GoodsType
	}
	if in.GoodsType != nil {
		kind = *in.GoodsType
	}
	if kind != "physical" && kind != "virtual" {
		return fmt.Errorf("商品类型无效")
	}
	if p != nil && kind != p.GoodsType {
		return fmt.Errorf("已保存的商品不能改变虚拟或实体属性，请新建商品")
	}
	if kind != "physical" {
		return nil
	}
	if p != nil && (p.UpstreamSourceID > 0 || len(p.DirectContent) > 0 || p.FulfillmentMode == "local" || p.FulfillmentMode == "reuse") {
		return fmt.Errorf("该商品已有虚拟交付配置，请新建实体商品")
	}
	if len(in.DirectContent) > 0 || (p != nil && p.PointsRequired > 0 && !in.PointsRequiredSet) {
		return fmt.Errorf("实体商品不能配置虚拟内容或积分兑换")
	}
	if in.PointsRequired > 0 {
		return fmt.Errorf("实体商品暂不支持积分兑换")
	}
	if in.FulfillmentMode != "" && in.FulfillmentMode != "auto" && in.FulfillmentMode != "shipping" {
		return fmt.Errorf("实体商品只支持快递发货")
	}
	in.FulfillmentMode = "auto"
	in.StockType = "card"
	mode := "free"
	fee := int64(0)
	countries := in.ShippingCountries
	if p != nil {
		mode = p.ShippingMode
		fee = p.ShippingFee
		if in.GoodsType == nil && in.ShippingMode == nil {
			countries = p.ShippingCountries
		}
	}
	if in.ShippingMode != nil {
		mode = *in.ShippingMode
	}
	if in.ShippingFee != nil {
		fee = *in.ShippingFee
	}
	if mode != "free" && mode != "fixed" {
		return fmt.Errorf("运费方式无效")
	}
	if !money.ValidCents(fee) {
		return fmt.Errorf("运费金额无效")
	}
	if mode == "free" {
		fee = 0
	}
	if mode == "fixed" && fee == 0 {
		return fmt.Errorf("固定运费必须大于0；免运费请选择包邮")
	}
	if len(countries) == 0 {
		return fmt.Errorf("请至少选择一个可配送国家或地区")
	}
	seen := map[string]bool{}
	for _, c := range countries {
		if _, ok := shipping.Regions[c]; !ok || seen[c] {
			return fmt.Errorf("配送国家无效或重复")
		}
		seen[c] = true
	}
	if in.PhysicalStock != nil && (*in.PhysicalStock < 0 || *in.PhysicalStock > 100000000) {
		return fmt.Errorf("实物库存无效")
	}
	in.ShippingMode = &mode
	in.ShippingFee = &fee
	in.ShippingCountries = countries
	return nil
}
