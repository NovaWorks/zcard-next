package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/go-kratos/kratos/v3/errors"
)

type gatewayAttempt struct {
	Lease      string             `json:"lease,omitempty"`
	LeaseUntil time.Time          `json:"lease_until,omitempty"`
	Info       *port.RedirectInfo `json:"info,omitempty"`
}

// Serialize gateway creation across tabs/processes, without holding a DB transaction
// during network I/O. Recovery always uses the original gateway idempotency key.
func (r *PaymentRepoImpl) dispatchPayment(ctx context.Context, p *ent.Payment, provider port.Provider, req port.CreatePaymentRequest) (*port.RedirectInfo, error) {
	lease, err := bepusdtNonce()
	if err != nil {
		return nil, err
	}
	var cached *port.RedirectInfo
	err = data.Tx(ctx, r.data, func(tx context.Context) error {
		c := data.Client(tx, r.data)
		var current *ent.Payment
		var e error
		if r.data.Dialect == db.SQLite {
			current, e = c.Payment.UpdateOneID(p.ID).SetDriverSnapshot(p.DriverSnapshot).Save(tx)
		} else {
			current, e = c.Payment.Query().Where(payment.ID(p.ID)).ForUpdate().Only(tx)
		}
		if e != nil {
			return e
		}
		if current.Status != payment.StatusPending {
			return errors.BadRequest("payment.NOT_PENDING", "支付已处理，请刷新查看结果")
		}
		if !current.ExpiresAt.IsZero() && !time.Now().Before(current.ExpiresAt) {
			return errors.BadRequest("payment.ORDER_EXPIRED", "支付已过期")
		}
		state := gatewayAttempt{}
		if len(current.GatewayContext) > 0 && json.Unmarshal(current.GatewayContext, &state) != nil {
			return fmt.Errorf("payment.ATTEMPT_INVALID")
		}
		if state.Info != nil {
			cached = state.Info
			return nil
		}
		if time.Now().Before(state.LeaseUntil) {
			return errors.BadRequest("payment.IN_PROGRESS", "正在发起支付，请稍后重试")
		}
		state.Lease = lease
		state.LeaseUntil = time.Now().Add(time.Minute)
		raw, _ := json.Marshal(state)
		return c.Payment.Update().Where(payment.ID(p.ID)).SetGatewayContext(raw).Exec(tx)
	})
	if err != nil {
		return nil, err
	}
	if cached != nil {
		return cached, nil
	}
	req.IdempotencyKey = p.GatewayOrderRef
	info, createErr := provider.CreatePayment(ctx, req)
	if createErr == nil && info == nil {
		createErr = fmt.Errorf("payment.EMPTY_GATEWAY_RESPONSE")
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err = data.Tx(saveCtx, r.data, func(tx context.Context) error {
		c := data.Client(tx, r.data)
		current, e := c.Payment.UpdateOneID(p.ID).SetDriverSnapshot(p.DriverSnapshot).Save(tx)
		if e != nil {
			return e
		}
		state := gatewayAttempt{}
		if json.Unmarshal(current.GatewayContext, &state) != nil || state.Lease != lease {
			return fmt.Errorf("payment.ATTEMPT_CHANGED")
		}
		state.Lease = ""
		state.LeaseUntil = time.Time{}
		update := c.Payment.Update().Where(payment.ID(p.ID))
		if createErr == nil {
			state.Info = info
			if current.Status == payment.StatusPending && info.ChannelOrderNo != "" {
				if current.ChannelOrderNo != "" && current.ChannelOrderNo != info.ChannelOrderNo {
					return fmt.Errorf("payment.GATEWAY_ORDER_MISMATCH")
				}
				update.SetChannelOrderNo(info.ChannelOrderNo)
			}
		}
		raw, _ := json.Marshal(state)
		return update.SetGatewayContext(raw).Exec(tx)
	})
	if err != nil {
		return nil, err
	}
	return info, createErr
}
