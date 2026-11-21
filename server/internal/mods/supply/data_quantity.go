package supply

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

func (g *Gateway) CheckQuantity(ctx context.Context, connection uint64, code, sku string, quantity int) (bool, error) {
	if quantity < 1 {
		return false, fmt.Errorf("购买数量无效")
	}
	conn, err := g.gate(ctx, connection)
	if err != nil {
		return false, err
	}
	if string(conn.Status) != "active" {
		return false, fmt.Errorf("货源已停用")
	}
	a, err := g.adapterFor(ctx, connection)
	if err != nil {
		return false, err
	}
	if q, ok := a.(interface {
		CheckQuantity(context.Context, string, string, int) (bool, error)
	}); ok {
		if a.Protocol() == "zcard" {
			n, e := a.GetStock(ctx, code, sku)
			if e == nil && n >= -1 {
				return n == -1 || int64(n) >= int64(quantity), nil
			}
		}
		available, e := q.CheckQuantity(ctx, code, sku, quantity)
		g.feedback(ctx, conn, e)
		return available, e
	}
	n, err := a.GetStock(ctx, code, sku)
	g.feedback(ctx, conn, err)
	if err != nil || n < -1 {
		return false, fmt.Errorf("暂时无法确认库存")
	}
	return n == -1 || int64(n) >= int64(quantity), nil
}
func (g *Gateway) DisplayAvailability(ctx context.Context, connection uint64, code, sku string, quantity int) (bool, error) {
	// Only providers with a dedicated quantity endpoint can bypass missing counts.
	a, err := g.adapterFor(ctx, connection)
	if err != nil {
		return false, err
	}
	if _, ok := a.(interface {
		CheckQuantity(context.Context, string, string, int) (bool, error)
	}); !ok {
		return false, adapter.ErrNotSupported
	}
	return g.CheckQuantity(ctx, connection, code, sku, quantity)
}
