package procurement

import (
	"context"
	"fmt"
	"math/big"
	"net/url"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
)

func (s *AdminProcurementService) detail(ctx context.Context, po *ent.ProcurementOrder) (*adminv1.ProcurementOrder, error) {
	c := data.Client(ctx, s.repo.data)
	item, err := c.OrderItem.Query().Where(orderitem.ID(po.OrderItemID), orderitem.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("procurement.ORDER_NOT_FOUND", "关联订单不存在")
	}
	if err != nil {
		return nil, err
	}
	o, err := c.Order.Get(ctx, item.OrderID)
	if err != nil {
		return nil, err
	}
	out := s.toProto(ctx, po)
	out.OrderNo = o.OrderNo
	out.OrderStatus = string(o.Status)
	out.ProductId = item.ProductID
	out.SkuName = item.SkuName
	out.ItemQuantity = item.Quantity
	out.SaleUnitCents = item.UnitPrice
	out.SaleAmountCents = item.Amount
	out.CostUnitCents = item.Cost
	out.CostTotalCents = item.Cost * int64(item.Quantity)
	out.CostBasis = "unrecorded"
	if item.Cost > 0 {
		out.CostBasis = "order_snapshot"
	}
	items, err := c.OrderItem.Query().Where(orderitem.OrderID(o.ID)).Order(ent.Asc(orderitem.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	out.AllocatedSaleCents = allocatedSale(o.TotalAmount, items, item.ID)
	if out.CostBasis == "order_snapshot" {
		out.ProfitCents = out.AllocatedSaleCents - out.CostTotalCents
	}
	out.ProductName = fmt.Sprintf("商品 #%d（已不可用）", item.ProductID)
	if p, e := c.Product.Get(ctx, item.ProductID); e == nil {
		out.ProductName = p.Name
	} else if !ent.IsNotFound(e) {
		return nil, e
	}
	if conn, e := c.SupplyConnection.Get(ctx, po.ConnectionID); e == nil {
		out.ConnectionName = conn.Name
		out.ConnectionDriver = conn.Driver
		if u, e := url.Parse(conn.BaseURL); e == nil {
			u.User = nil
			u.RawQuery = ""
			u.Fragment = ""
			out.ConnectionUrl = u.String()
		}
	} else if !ent.IsNotFound(e) {
		return nil, e
	}
	if pi, e := s.repo.ItemByProcurement(ctx, po.ID); e == nil {
		out.UpstreamProductCode = pi.UpstreamSku
	} else if !ent.IsNotFound(e) {
		return nil, e
	}
	return out, nil
}

// 累计比例分摊避免逐项四舍五入造成整单差一分；大整数避免金额乘法溢出。
func allocatedSale(total int64, items []*ent.OrderItem, target uint64) int64 {
	sum := new(big.Int)
	for _, it := range items {
		sum.Add(sum, big.NewInt(it.Amount))
	}
	if sum.Sign() <= 0 {
		return 0
	}
	before := new(big.Int)
	for _, it := range items {
		after := new(big.Int).Add(before, big.NewInt(it.Amount))
		if it.ID == target {
			lo := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(total), before), sum)
			hi := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(total), after), sum)
			return hi.Sub(hi, lo).Int64()
		}
		before = after
	}
	return 0
}
