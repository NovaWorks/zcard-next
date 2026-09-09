package payment

import (
	"context"
	"fmt"
	"strconv"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/paymentchannel"
)

// Prefer the tenant's own channel, then an explicitly shared main-site channel.
func resolveChannel(ctx context.Context, d *data.Data, subsite uint64, code string) (*ent.PaymentChannel, error) {
	c := data.Client(ctx, d)
	ch, err := c.PaymentChannel.Query().Where(paymentchannel.SubsiteID(subsite), paymentchannel.Code(code)).Only(ctx)
	if ent.IsNotFound(err) && subsite != 0 {
		return c.PaymentChannel.Query().Where(paymentchannel.SubsiteID(0), paymentchannel.Code(code)).Only(ctx)
	}
	return ch, err
}

func (r *PaymentRepoImpl) channelForPayment(ctx context.Context, p *ent.Payment) (*ent.PaymentChannel, error) {
	if p.ChannelID != 0 {
		ch, err := data.Client(ctx, r.data).PaymentChannel.Get(ctx, p.ChannelID)
		if err != nil {
			return nil, err
		}
		if ch.Code != p.Channel || (p.DriverSnapshot != "" && ch.Driver != p.DriverSnapshot) {
			return nil, fmt.Errorf("payment.CHANNEL_CHANGED: 请核对历史支付渠道")
		}
		return ch, nil
	}
	tenant := p.SubsiteID
	if p.OrderID != 0 {
		o, err := data.Client(ctx, r.data).Order.Get(ctx, p.OrderID)
		if err != nil {
			return nil, err
		}
		tenant = o.SubsiteID
	}
	return resolveChannel(ctx, r.data, tenant, p.Channel)
}

func (r *PaymentRepoImpl) callbackURLFor(ctx context.Context, ch *ent.PaymentChannel) string {
	return r.CallbackURL(ctx, ch.Code) + "?channel_id=" + strconv.FormatUint(ch.ID, 10)
}

// Old callback URLs remain usable only when the code identifies one channel.
func callbackChannel(ctx context.Context, d *data.Data, code, id string) (*ent.PaymentChannel, error) {
	q := data.Client(ctx, d).PaymentChannel.Query().Where(paymentchannel.Code(code))
	if id != "" {
		n, err := strconv.ParseUint(id, 10, 64)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("payment.CHANNEL_INVALID")
		}
		q.Where(paymentchannel.ID(n))
	}
	return q.Only(ctx)
}
