package dashboard

import (
	"context"
	"fmt"
	"math/big"
	"sort"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/paymentchannel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// Gross order payments only: recharge and unallocated/review receipts are separate.
// Historical channel identities remain reportable after deletion.
func (r *DashboardRepoImpl) GetTopChannels(ctx context.Context, window ...int) ([]TopChannel, error) {
	days := 30
	if len(window) > 0 {
		days = window[0]
	}
	start, end := reportWindow(r.now(), days)
	c := data.Client(ctx, r.data)
	subsite := tenancy.FromContext(ctx).SubsiteID
	rows, err := c.Payment.Query().Where(payment.SubsiteID(subsite), payment.StatusEQ(payment.StatusSuccess), payment.ReviewReasonEQ(""), payment.OrderIDNEQ(0),
		payment.HasOrderWith(order.SubsiteID(subsite), order.AdminDeletedAtIsNil(), order.PaidAtGTE(start), order.PaidAtLT(end))).
		Select(payment.FieldChannelID, payment.FieldChannel, payment.FieldOrderID).Order(ent.Asc(payment.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	amounts := map[uint64]int64{}
	ids := []uint64{}
	seen := map[uint64]bool{}
	for _, p := range rows {
		if !seen[p.OrderID] {
			ids = append(ids, p.OrderID)
			seen[p.OrderID] = true
		}
	}
	for i := 0; i < len(ids); i += 500 {
		os, err := c.Order.Query().Where(order.IDIn(ids[i:min(i+500, len(ids))]...)).Select(order.FieldTotalAmount).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range os {
			amounts[o.ID] = o.TotalAmount
		}
	}
	groups := map[string]*TopChannel{}
	seen = map[uint64]bool{}
	for _, p := range rows {
		if seen[p.OrderID] {
			continue
		}
		seen[p.OrderID] = true
		key := fmt.Sprintf("%d:%s", p.ChannelID, p.Channel)
		g := groups[key]
		if g == nil {
			g = &TopChannel{ChannelID: p.ChannelID, Channel: p.Channel, Name: p.Channel, ChannelState: "legacy"}
			groups[key] = g
		}
		g.TotalCount++
		g.SuccessCount++
		g.Amount += amounts[p.OrderID]
	}
	// Channel configs are never selected into the report.
	channels, err := c.PaymentChannel.Query().Where(paymentchannel.SubsiteIDIn(0, subsite)).Select(paymentchannel.FieldName, paymentchannel.FieldCode, paymentchannel.FieldEnabled, paymentchannel.FieldDeletedAt, paymentchannel.FieldSubsiteID).All(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[uint64]*ent.PaymentChannel{}
	for _, ch := range channels {
		byID[ch.ID] = ch
	}
	out := make([]TopChannel, 0, len(groups))
	for _, g := range groups {
		if ch := byID[g.ChannelID]; ch != nil && ch.Code == g.Channel {
			g.Name = ch.Name
			g.ChannelState = "active"
			if !ch.Enabled {
				g.ChannelState = "disabled"
			}
			if !ch.DeletedAt.IsZero() {
				g.ChannelState = "deleted"
			}
		}
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Amount != out[j].Amount {
			return out[i].Amount > out[j].Amount
		}
		if out[i].ChannelID != out[j].ChannelID {
			return out[i].ChannelID < out[j].ChannelID
		}
		return out[i].Channel < out[j].Channel
	})
	return out, nil
}

// Distribute the order's final paid amount (including order-level coupons) over
// its items; integer remainder goes to the last item so totals stay exact.
func (r *DashboardRepoImpl) GetTopProducts(ctx context.Context, window ...int) ([]TopProduct, error) {
	days := 30
	if len(window) > 0 {
		days = window[0]
	}
	start, end := reportWindow(r.now(), days)
	l, err := r.loadLedger(ctx, tenancy.FromContext(ctx).SubsiteID, start, end)
	if err != nil {
		return nil, err
	}
	groups := map[uint64]*TopProduct{}
	for _, o := range l.orders {
		if !within(o.PaidAt, start, end) {
			continue
		}
		items := l.items[o.ID]
		var weight int64
		for _, it := range items {
			weight += max(it.Amount, 0)
		}
		remaining := o.TotalAmount
		for i, it := range items {
			amount := int64(0)
			if i == len(items)-1 {
				amount = remaining
			} else if weight > 0 {
				v := new(big.Int).Mul(big.NewInt(o.TotalAmount), big.NewInt(max(it.Amount, 0)))
				amount = v.Div(v, big.NewInt(weight)).Int64()
			}
			remaining -= amount
			g := groups[it.ProductID]
			if g == nil {
				g = &TopProduct{ProductID: it.ProductID, Name: fmt.Sprintf("历史商品 #%d", it.ProductID)}
				groups[it.ProductID] = g
			}
			g.SoldQty += int64(it.Quantity)
			g.Revenue += amount
		}
	}
	out := make([]TopProduct, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Revenue != out[j].Revenue {
			return out[i].Revenue > out[j].Revenue
		}
		return out[i].ProductID < out[j].ProductID
	})
	if len(out) > 5 {
		out = out[:5]
	}
	ids := []uint64{}
	for _, g := range out {
		ids = append(ids, g.ProductID)
	}
	if len(ids) > 0 {
		ps, err := data.Client(ctx, r.data).Product.Query().Where(product.IDIn(ids...)).Select(product.FieldName).All(ctx)
		if err != nil {
			return nil, err
		}
		for i := range out {
			for _, p := range ps {
				if p.ID == out[i].ProductID {
					out[i].Name = p.Name
				}
			}
		}
	}
	return out, nil
}

func (r *DashboardRepoImpl) PaymentReviewCount(ctx context.Context) (int64, error) {
	n, err := data.Client(ctx, r.data).Payment.Query().Where(payment.SubsiteID(tenancy.FromContext(ctx).SubsiteID), payment.ReviewReasonNEQ("")).Count(ctx)
	return int64(n), err
}
