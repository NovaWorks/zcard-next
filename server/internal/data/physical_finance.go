package data

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"math/big"
)

func SnapshotInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}
func Prorate(total, part, base int64) int64 {
	if total <= 0 || part <= 0 || base <= 0 {
		return 0
	}
	if part >= base {
		return total
	}
	v := new(big.Int).Mul(big.NewInt(total), big.NewInt(part))
	return v.Quo(v, big.NewInt(base)).Int64()
}
func LockFinancialOrder(ctx context.Context, d *Data, id uint64) (*ent.Order, error) {
	c := Client(ctx, d)
	o, e := c.Order.Get(ctx, id)
	if e != nil {
		return nil, e
	}
	n, e := c.Order.Update().Where(order.ID(id), order.Version(o.Version)).AddVersion(1).Save(ctx)
	if e != nil {
		return nil, e
	}
	if n != 1 {
		return nil, fmt.Errorf("订单账务已变化，请重试")
	}
	if o.Extra == nil {
		o.Extra = map[string]any{}
	}
	return o, nil
}

// Durable totals, independent of refund-event delivery order. Shipping never earns rewards.
func PhysicalRefundTotals(ctx context.Context, d *Data, id uint64) (goods, refunded, markup int64, err error) {
	rows, e := Client(ctx, d).OrderItem.Query().Where(orderitem.OrderID(id)).All(ctx)
	if e != nil {
		return 0, 0, 0, e
	}
	for _, it := range rows {
		goods += it.PaidAmount
		refunded += it.RefundedAmount
		markup += Prorate(SnapshotInt(it.ProfitSnapshot["markup"]), it.RefundedAmount, it.PaidAmount)
	}
	return
}
