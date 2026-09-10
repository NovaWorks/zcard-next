package payment

import (
	"context"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
)

// slowDrivers 慢支付渠道驱动名单（usdt 族）。
var slowDrivers = map[string]bool{
	"epusdt": true,
	"usdt":   true,
}

// HasPendingSlowPayment bounds waiting by the order deadline, never by retry time.
func (r *PaymentRepoImpl) HasPendingSlowPayment(ctx context.Context, orderID uint64) (bool, error) {
	client := data.Client(ctx, r.data)
	pays, err := client.Payment.Query().
		Where(payment.OrderID(orderID), payment.StatusEQ(payment.StatusPending)).
		All(ctx)
	if err != nil {
		return false, err
	}
	if len(pays) == 0 {
		return false, nil
	}
	hasSlow := false
	for _, p := range pays {
		driver := p.DriverSnapshot
		if driver == "" {
			ch, err := r.channelForPayment(ctx, p)
			if err != nil {
				return false, err
			}
			driver = ch.Driver
		}
		if slowDrivers[driver] {
			hasSlow = true
		}
	}
	if !hasSlow {
		return false, nil
	}
	o, err := client.Order.Get(ctx, orderID)
	if err != nil {
		return false, err
	}
	if o.ExpiredAt.IsZero() {
		return false, fmt.Errorf("payment.MISSING_ORDER_DEADLINE")
	}
	return time.Now().UTC().Before(o.ExpiredAt.Add(port.SlowPaymentGracePeriod)), nil
}
