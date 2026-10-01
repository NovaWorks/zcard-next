package payment

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
)

func orderBaseCurrency(o *ent.Order) string {
	if o.BaseCurrency == "" {
		return data.DefaultBaseCurrency
	}
	return o.BaseCurrency
}
func (r *PaymentRepoImpl) validateOrderCurrency(ctx context.Context, o *ent.Order) error {
	base, err := data.BaseCurrency(ctx, r.data)
	if err != nil {
		return err
	}
	if orderBaseCurrency(o) != base {
		return fmt.Errorf("payment.CURRENCY_MISMATCH: 订单币种与基础货币不一致，需要迁移")
	}
	return nil
}
func (r *PaymentRepoImpl) paymentBaseCurrency(ctx context.Context, p *ent.Payment) (string, error) {
	if code := pricingOf(p).BaseCurrency; code != "" {
		return code, nil
	}
	if p.OrderID != 0 {
		o, err := data.Client(ctx, r.data).Order.Get(ctx, p.OrderID)
		if err != nil {
			return "", err
		}
		return orderBaseCurrency(o), nil
	}
	// Recharge attempts without a pricing currency predate configurable settlement.
	return data.DefaultBaseCurrency, nil
}
