package payment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/rechargeorder"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// Contains no API token. Channel credentials cannot change while an attempt is unresolved.
type bepusdtAttempt struct {
	Subject    string    `json:"subject"`
	NotifyURL  string    `json:"notify_url"`
	ReturnURL  string    `json:"return_url"`
	PaymentURL string    `json:"payment_url,omitempty"`
	Lease      string    `json:"lease,omitempty"`
	LeaseUntil time.Time `json:"lease_until,omitempty"`
}

func encodeBepusdtAttempt(a bepusdtAttempt) json.RawMessage { b, _ := json.Marshal(a); return b }
func bepusdtNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// The channel row serializes attempt creation and credential edits across processes.
// The network call happens after commit. A durable lease prevents concurrent HTTP
// requests; after a crash the next request recovers using the same merchant order ID.
func (r *PaymentRepoImpl) createBepusdtPayment(ctx context.Context, chID, orderID, rechargeID uint64, method string) (*port.RechargePaymentInfo, error) {
	var p *ent.Payment
	var cfg json.RawMessage
	var a bepusdtAttempt
	lease, err := bepusdtNonce()
	if err != nil {
		return nil, err
	}
	cached := false
	err = data.Tx(ctx, r.data, func(txCtx context.Context) error {
		c := data.Client(txCtx, r.data)
		ch, err := c.PaymentChannel.UpdateOneID(chID).AddSort(0).Save(txCtx)
		if err != nil {
			return err
		}
		if ch.Driver != "bepusdt" || !ch.Enabled || !ch.DeletedAt.IsZero() {
			return fmt.Errorf("payment.CHANNEL_DISABLED")
		}
		cfg = r.DecryptConfig(ch)
		conf, err := adapter.ParseBepusdtConfig(cfg)
		if err != nil {
			return err
		}
		if len(ch.Methods) > 0 {
			return fmt.Errorf("payment.METHODS_INVALID: BEpusdt 每个渠道固定一条链")
		}
		var amount int64
		var subsite uint64
		scope := ""
		deadline := time.Now().UTC().Add(time.Duration(conf.Timeout) * time.Second)
		q := c.Payment.Query().Where(payment.ChannelID(chID), payment.DriverSnapshot("bepusdt"))
		if orderID > 0 && rechargeID == 0 {
			o, err := c.Order.UpdateOneID(orderID).AddVersion(1).Save(txCtx)
			if err != nil {
				return err
			}
			if o.Status != order.StatusPendingPayment || o.ExpiryReview || !time.Now().Before(o.ExpiredAt) {
				return fmt.Errorf("payment.ORDER_EXPIRED: 订单已关闭或待核对")
			}
			if err := checkPaymentUsage(ch, method, scenePurchase); err != nil {
				return err
			}
			amount, subsite = o.TotalAmount, o.SubsiteID
			scope = o.OrderNo
			if o.ExpiredAt.Before(deadline) {
				deadline = o.ExpiredAt
			}
			a.Subject = "订单 " + o.OrderNo
			a.ReturnURL = absolutePayURL(ctx, "/payment/"+o.OrderNo)
			q.Where(payment.OrderID(orderID))
		} else if rechargeID > 0 && orderID == 0 {
			ro, err := c.RechargeOrder.Get(txCtx, rechargeID)
			if err != nil {
				return err
			}
			if ro.Status != rechargeorder.StatusPending {
				return fmt.Errorf("payment.RECHARGE_NOT_PENDING")
			}
			scene := sceneMemberRecharge
			if ro.Target == rechargeorder.TargetSupply {
				scene = sceneSupplyRecharge
			}
			if err := checkPaymentUsage(ch, method, scene); err != nil {
				return err
			}
			amount = ro.Amount
			scope = scene
			a.Subject = "余额充值"
			a.ReturnURL = absolutePayURL(ctx, "/member?tab=recharge")
			q.Where(payment.RechargeOrderID(rechargeID))
		} else {
			return fmt.Errorf("payment.INVALID_INPUT")
		}
		if amount <= 0 {
			return fmt.Errorf("payment.INVALID_CHARGE_AMOUNT")
		}
		p, err = q.Order(ent.Desc(payment.FieldID)).First(txCtx)
		if ent.IsNotFound(err) {
			if time.Until(deadline) <= 179*time.Second {
				return fmt.Errorf("payment.ORDER_EXPIRED: 剩余支付时间不足 180 秒，请重新下单")
			}
			ref, err := bepusdtNonce()
			if err != nil {
				return err
			}
			a.NotifyURL = absolutePayURL(ctx, r.callbackURLFor(ctx, ch))
			price, e := r.price(txCtx, ch, amount, method)
			if e != nil {
				return e
			}
			if e = checkQuote(ctx, pricingQuote(price, ch.Code, ch.ID, scope, 0)); e != nil {
				return e
			}
			b := c.Payment.Create().SetPricingSnapshot(pricingJSON(price)).SetFee(price.Fee).SetSubsiteID(subsite).SetChannelID(ch.ID).SetDriverSnapshot(ch.Driver).SetChannel(ch.Code).SetAmount(price.Total).SetExpiresAt(deadline).SetGatewayOrderRef("BE" + ref).SetStatus(payment.StatusPending)
			if orderID > 0 {
				b.SetOrderID(orderID)
			} else {
				b.SetRechargeOrderID(rechargeID)
			}
			p, err = b.Save(txCtx)
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if p.Status != payment.StatusPending {
				return fmt.Errorf("payment.NOT_PENDING: 支付已处理，请刷新页面")
			}
			if pricingOf(p).Base != amount {
				return fmt.Errorf("payment.AMOUNT_MISMATCH")
			}
			if e := checkQuote(ctx, pricingQuote(pricingOf(p), ch.Code, ch.ID, scope, p.ID)); e != nil {
				if e = checkQuote(ctx, pricingQuote(pricingOf(p), ch.Code, ch.ID, scope, 0)); e != nil {
					return e
				}
			}
			if json.Unmarshal(p.GatewayContext, &a) != nil {
				return fmt.Errorf("payment.ATTEMPT_INVALID")
			}
			if !time.Now().Before(p.ExpiresAt) {
				return fmt.Errorf("payment.ORDER_EXPIRED: 支付已过期，请重新下单")
			}
			if a.PaymentURL != "" {
				cached = true
				return nil
			}
			if time.Now().Before(a.LeaseUntil) {
				return fmt.Errorf("payment.IN_PROGRESS: 正在发起支付，请稍后重试")
			}
			if time.Until(p.ExpiresAt) <= 179*time.Second {
				return fmt.Errorf("payment.ORDER_EXPIRED: 剩余支付时间不足 180 秒，请重新下单")
			}
		}
		a.Lease = lease
		a.LeaseUntil = time.Now().UTC().Add(time.Minute)
		_, err = c.Payment.UpdateOneID(p.ID).SetGatewayContext(encodeBepusdtAttempt(a)).Save(txCtx)
		return err
	})
	if err != nil {
		return nil, err
	}
	if cached {
		return &port.RechargePaymentInfo{BaseCents: pricingOf(p).Base, FeeCents: p.Fee, TotalCents: p.Amount, PaymentID: p.ID, Type: "redirect", Payload: a.PaymentURL}, nil
	}
	provider, err := r.reg.Provider("bepusdt")
	if err != nil {
		return nil, err
	}
	info, createErr := provider.CreatePayment(ctx, port.CreatePaymentRequest{GatewayOrderRef: p.GatewayOrderRef, Deadline: p.ExpiresAt, Amount: money.Cents(p.Amount), Channel: p.Channel, Subject: a.Subject, ReturnURL: a.ReturnURL, NotifyBaseURL: a.NotifyURL, Config: cfg})
	// Release the lease even when the caller disconnected. An unknown response does
	// not discard the attempt: retry uses the persisted reference and original deadline.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err = data.Tx(saveCtx, r.data, func(txCtx context.Context) error {
		c := data.Client(txCtx, r.data)
		current, err := r.lockBepusdtPayment(txCtx, p.ID)
		if err != nil {
			return err
		}
		var state bepusdtAttempt
		if json.Unmarshal(current.GatewayContext, &state) != nil || state.Lease != lease {
			return fmt.Errorf("payment.ATTEMPT_CHANGED")
		}
		state.Lease = ""
		state.LeaseUntil = time.Time{}
		update := c.Payment.UpdateOneID(p.ID)
		if createErr == nil {
			if current.ChannelOrderNo != "" && current.ChannelOrderNo != info.ChannelOrderNo {
				return fmt.Errorf("payment.GATEWAY_ORDER_MISMATCH")
			}
			state.PaymentURL = string(info.Payload)
			update.SetChannelOrderNo(info.ChannelOrderNo)
			if !info.Deadline.IsZero() && info.Deadline.Before(current.ExpiresAt) {
				update.SetExpiresAt(info.Deadline)
			}
		}
		_, err = update.SetGatewayContext(encodeBepusdtAttempt(state)).Save(txCtx)
		return err
	})
	if err != nil {
		return nil, err
	}
	if createErr != nil {
		return nil, createErr
	}
	if orderID > 0 {
		o, err := data.Client(ctx, r.data).Order.Get(ctx, orderID)
		if err != nil {
			return nil, err
		}
		if o.Status != order.StatusPendingPayment || !time.Now().Before(o.ExpiredAt) {
			return nil, fmt.Errorf("payment.ORDER_NOT_PENDING: 订单状态已变化，请刷新页面查看结果")
		}
	}
	return &port.RechargePaymentInfo{BaseCents: pricingOf(p).Base, FeeCents: p.Fee, TotalCents: p.Amount, PaymentID: p.ID, Type: info.Type, Payload: string(info.Payload)}, nil
}

func (r *PaymentRepoImpl) checkBepusdtConfigChange(ctx context.Context, ch *ent.PaymentChannel, raw string) error {
	if ch.Driver != "bepusdt" {
		return nil
	}
	next, err := adapter.ParseBepusdtConfig(json.RawMessage(raw))
	if err != nil {
		return err
	}
	old, err := adapter.ParseBepusdtConfig(r.DecryptConfig(ch))
	if err == nil && old == next {
		return nil
	}
	pending, err := data.Client(ctx, r.data).Payment.Query().Where(payment.ChannelID(ch.ID), payment.Or(payment.StatusEQ(payment.StatusPending), payment.ReviewReasonNEQ(""))).Exist(ctx)
	if err != nil {
		return err
	}
	if pending {
		return fmt.Errorf("payment.CHANNEL_BUSY: 此 BEpusdt 渠道仍有未确认支付，请保留原配置处理回调；更换网关或网络请新建渠道")
	}
	return nil
}

// MySQL REPEATABLE READ may keep a stale snapshot after a no-op UPDATE.
// Always use a locking read when supported; SQLite serializes writers instead.
func (r *PaymentRepoImpl) lockBepusdtPayment(ctx context.Context, id uint64) (*ent.Payment, error) {
	c := data.Client(ctx, r.data)
	if r.data.Dialect != db.SQLite {
		return c.Payment.Query().Where(payment.ID(id)).ForUpdate().Only(ctx)
	}
	return c.Payment.UpdateOneID(id).SetDriverSnapshot("bepusdt").Save(ctx)
}
