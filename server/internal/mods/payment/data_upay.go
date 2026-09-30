package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/adapter"
)

// Each UPAY network keeps its own stable reference and checkout. Legacy attempts
// without a method snapshot belong to the original fixed trade_type.
func (r *PaymentRepoImpl) nativeAttempt(ctx context.Context, q *ent.PaymentQuery, ch *ent.PaymentChannel, method string) (*ent.Payment, error) {
	q.Order(ent.Desc(payment.FieldID))
	if ch.Driver != "upay" {
		return q.First(ctx)
	}
	cfg, err := adapter.ParseUpayConfig(r.DecryptConfig(ch))
	if err != nil {
		return nil, err
	}
	selected, err := cfg.SelectTrade(method)
	if err != nil {
		return nil, err
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	var match *ent.Payment
	for _, row := range rows {
		trade := pricingOf(row).Method
		if trade == "" {
			trade = cfg.TradeType
		}
		if trade == selected {
			if match == nil {
				match = row
			}
			continue
		}
		// Switching networks must not bypass a still running or uncertain request.
		if row.Status == payment.StatusPending {
			var attempt bepusdtAttempt
			if json.Unmarshal(row.GatewayContext, &attempt) != nil {
				return nil, fmt.Errorf("payment.ATTEMPT_INVALID")
			}
			if attempt.RequestSent && attempt.PaymentURL == "" {
				if time.Now().Before(attempt.LeaseUntil) {
					return nil, fmt.Errorf("payment.IN_PROGRESS: 正在发起支付，请稍后重试")
				}
				return nil, fmt.Errorf("payment.RESULT_UNKNOWN: UPAY PRO 原支付结果待核对，网关商户单号 %s；请勿换链重复付款", row.GatewayOrderRef)
			}
		}
	}
	if match == nil {
		return nil, &ent.NotFoundError{}
	}
	return match, nil
}
