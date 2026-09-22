package dashboard

import (
	"context"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/platform/businessday"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// All dashboard windows use business-calendar dates and the same captured now.
func reportDays(days int) int {
	if days == 1 || days == 14 || days == 30 {
		return days
	}
	return 7
}
func reportWindow(now time.Time, days int) (time.Time, time.Time) {
	return businessday.Start(now).AddDate(0, 0, -(reportDays(days) - 1)).UTC(), now.UTC()
}
func within(at, start, end time.Time) bool {
	return !at.IsZero() && !at.Before(start) && at.Before(end)
}

type metricLedger struct {
	orders  []*ent.Order
	refunds []*ent.RefundOrder
	items   map[uint64][]*ent.OrderItem
	users   []*ent.User
}

// Select only report columns. No payment raw bodies, credentials or order contacts.
// Refunds currently settle atomically when the succeeded receipt is created
// (RefundToWallet); created_at is that immutable settlement timestamp.
func (r *DashboardRepoImpl) loadLedger(ctx context.Context, subsite uint64, start, end time.Time) (*metricLedger, error) {
	c := data.Client(ctx, r.data)
	start, end = start.UTC(), end.UTC()
	refundWindow := refundorder.And(refundorder.StatusEQ(refundorder.StatusSucceeded), refundorder.CreatedAtGTE(start), refundorder.CreatedAtLT(end))
	orders, err := c.Order.Query().Where(order.SubsiteID(subsite), order.AdminDeletedAtIsNil(), order.Or(
		order.And(order.CreatedAtGTE(start), order.CreatedAtLT(end)), order.And(order.PaidAtGTE(start), order.PaidAtLT(end)), order.HasRefundsWith(refundWindow))).
		Select(order.FieldID, order.FieldCreatedAt, order.FieldPaidAt, order.FieldTotalAmount, order.FieldCost).All(ctx)
	if err != nil {
		return nil, err
	}
	refunds, err := c.RefundOrder.Query().Where(refundWindow, refundorder.HasOrderWith(order.SubsiteID(subsite), order.AdminDeletedAtIsNil(), order.PaidAtNotNil())).
		Select(refundorder.FieldOrderID, refundorder.FieldAmount, refundorder.FieldCreatedAt).All(ctx)
	if err != nil {
		return nil, err
	}
	l := &metricLedger{orders: orders, refunds: refunds, items: map[uint64][]*ent.OrderItem{}}
	ids := make([]uint64, 0, len(orders))
	for _, o := range orders {
		if within(o.PaidAt, start, end) {
			ids = append(ids, o.ID)
		}
	}
	// Bound IN lists for SQLite and avoid loading items for unpaid/canceled orders.
	for i := 0; i < len(ids); i += 500 {
		rows, err := c.OrderItem.Query().Where(orderitem.OrderIDIn(ids[i:min(i+500, len(ids))]...)).Select(orderitem.FieldID, orderitem.FieldOrderID, orderitem.FieldProductID, orderitem.FieldQuantity, orderitem.FieldAmount, orderitem.FieldCost).Order(ent.Asc(orderitem.FieldID)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, it := range rows {
			l.items[it.OrderID] = append(l.items[it.OrderID], it)
		}
	}
	l.users, err = c.User.Query().Where(user.CreatedAtGTE(start), user.CreatedAtLT(end)).Select(user.FieldCreatedAt).All(ctx)
	if err != nil {
		return nil, err
	}
	return l, nil
}
func (l *metricLedger) between(start, end time.Time) Metric {
	m := Metric{}
	for _, o := range l.orders {
		if within(o.CreatedAt, start, end) {
			m.Orders++
		}
		if !within(o.PaidAt, start, end) {
			continue
		}
		m.PaidOrders++
		m.Revenue += o.TotalAmount
		items := l.items[o.ID]
		unknown := false
		if len(items) == 0 {
			if o.Cost > 0 {
				m.Cost += o.Cost
			} else {
				unknown = true
			}
		} else {
			for _, it := range items {
				if it.Cost <= 0 || it.Quantity <= 0 {
					unknown = true
				} else {
					m.Cost += it.Cost * int64(it.Quantity)
				}
			}
		}
		if unknown {
			m.UnknownCostOrders++
		}
	}
	for _, rf := range l.refunds {
		if within(rf.CreatedAt, start, end) {
			m.Refunds += rf.Amount
		}
	}
	for _, u := range l.users {
		if within(u.CreatedAt, start, end) {
			m.NewUsers++
		}
	}
	m.NetRevenue = m.Revenue - m.Refunds
	m.Profit = m.NetRevenue - m.Cost
	if m.UnknownCostOrders > 0 {
		m.Profit = 0
	}
	return m
}
func (r *DashboardRepoImpl) GetOverview(ctx context.Context) (today, yesterday, last7d, prev7d, last30d, prev30d Metric, err error) {
	now := r.now().UTC()
	day := businessday.Start(now)
	start := day.AddDate(0, 0, -59)
	l, e := r.loadLedger(ctx, tenancy.FromContext(ctx).SubsiteID, start, now)
	if e != nil {
		err = e
		return
	}
	elapsed := now.Sub(day)
	today = l.between(day, now)
	yesterday = l.between(day.AddDate(0, 0, -1), day.AddDate(0, 0, -1).Add(elapsed))
	last7d = l.between(day.AddDate(0, 0, -6), now)
	prev7d = l.between(day.AddDate(0, 0, -13), day.AddDate(0, 0, -7).Add(elapsed))
	last30d = l.between(day.AddDate(0, 0, -29), now)
	prev30d = l.between(start, day.AddDate(0, 0, -30).Add(elapsed))
	return
}
func (r *DashboardRepoImpl) metricBetweenSubsite(ctx context.Context, subsite uint64, start, end time.Time) (Metric, error) {
	l, err := r.loadLedger(ctx, subsite, start, end)
	if err != nil {
		return Metric{}, err
	}
	return l.between(start, end), nil
}
func (r *DashboardRepoImpl) metricBetween(ctx context.Context, subsite uint64, start, end time.Time) (Metric, error) {
	return r.metricBetweenSubsite(ctx, subsite, start, end)
}
func (r *DashboardRepoImpl) GetTrend(ctx context.Context, days int) ([]TrendPoint, error) {
	start, end := reportWindow(r.now(), days)
	l, err := r.loadLedger(ctx, tenancy.FromContext(ctx).SubsiteID, start, end)
	if err != nil {
		return nil, err
	}
	points := make([]TrendPoint, 0, reportDays(days))
	for i := 0; i < reportDays(days); i++ {
		day := start.AddDate(0, 0, i)
		next := day.AddDate(0, 0, 1)
		if next.After(end) {
			next = end
		}
		m := l.between(day, next)
		points = append(points, TrendPoint{Date: businessday.Date(day, "2006-01-02"), Orders: m.Orders, Revenue: m.Revenue, PaidCount: m.PaidOrders, Cost: m.Cost, Profit: m.Profit, Refunds: m.Refunds, NetRevenue: m.NetRevenue})
	}
	return points, nil
}
