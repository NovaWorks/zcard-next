package dashboard

// 工作台指标聚合（M1b v1）：今日/近7天/近30天订单数与营收 + 趋势 + 商品 Top5。
// 金额口径：已支付订单（status 非 pending_payment/canceled/expired）的 total_amount 求和（分）。

import (
	"context"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
)

// Metric 统计项。
type Metric struct {
	Orders     int64
	Revenue    int64
	PaidOrders int64
}

// TrendPoint 趋势点。
type TrendPoint struct {
	Date    string
	Orders  int64
	Revenue int64
}

// TopProduct 商品排行。
type TopProduct struct {
	ProductID uint64
	Name      string
	SoldQty   int64
	Revenue   int64
}

// DashboardRepoImpl 报表仓储。
type DashboardRepoImpl struct {
	data *data.Data
}

// NewDashboardRepoImpl 构造。
func NewDashboardRepoImpl(d *data.Data) *DashboardRepoImpl {
	return &DashboardRepoImpl{data: d}
}

func paidStatuses() []order.Status {
	return []order.Status{
		order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered,
		order.StatusDelivered, order.StatusCompleted,
	}
}

// GetOverview 返回 today/last7d/last30d 三项统计。
func (r *DashboardRepoImpl) GetOverview(ctx context.Context) (today, last7d, last30d Metric, err error) {
	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if m, e := r.metricBetween(ctx, todayStart, now); e == nil {
		today = m
	}
	if m, e := r.metricBetween(ctx, now.AddDate(0, 0, -7), now); e == nil {
		last7d = m
	}
	if m, e := r.metricBetween(ctx, now.AddDate(0, 0, -30), now); e == nil {
		last30d = m
	}
	return today, last7d, last30d, nil
}

func (r *DashboardRepoImpl) metricBetween(ctx context.Context, start, end time.Time) (Metric, error) {
	client := data.Client(ctx, r.data)
	orders, err := client.Order.Query().
		Where(order.CreatedAtGTE(start), order.CreatedAtLTE(end)).
		All(ctx)
	if err != nil {
		return Metric{}, err
	}
	m := Metric{Orders: int64(len(orders))}
	paid := map[order.Status]bool{}
	for _, s := range paidStatuses() {
		paid[s] = true
	}
	for _, o := range orders {
		if paid[o.Status] {
			m.PaidOrders++
			m.Revenue += o.TotalAmount
		}
	}
	return m, nil
}

// GetTrend 近 7 天每日订单数与营收（含今日，共 7 个桶）。
func (r *DashboardRepoImpl) GetTrend(ctx context.Context) ([]TrendPoint, error) {
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -6)
	client := data.Client(ctx, r.data)
	rows, err := client.Order.Query().
		Where(order.CreatedAtGTE(start)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	paid := map[order.Status]bool{}
	for _, s := range paidStatuses() {
		paid[s] = true
	}
	// 初始化 7 天桶
	buckets := map[string]*TrendPoint{}
	for i := 0; i < 7; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		buckets[d] = &TrendPoint{Date: d}
	}
	for _, o := range rows {
		d := o.CreatedAt.Format("2006-01-02")
		bp, ok := buckets[d]
		if !ok {
			continue
		}
		bp.Orders++
		if paid[o.Status] {
			bp.Revenue += o.TotalAmount
		}
	}
	out := make([]TrendPoint, 0, 7)
	for i := 0; i < 7; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		out = append(out, *buckets[d])
	}
	return out, nil
}

// GetTopProducts 近 30 天销量 Top5。
func (r *DashboardRepoImpl) GetTopProducts(ctx context.Context) ([]TopProduct, error) {
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -30)
	client := data.Client(ctx, r.data)
	paidOrders, err := client.Order.Query().
		Where(order.CreatedAtGTE(start), order.StatusIn(paidStatuses()...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	orderIDs := make([]uint64, 0, len(paidOrders))
	for _, o := range paidOrders {
		orderIDs = append(orderIDs, o.ID)
	}
	if len(orderIDs) == 0 {
		return nil, nil
	}
	items, err := client.OrderItem.Query().
		Where(orderitem.OrderIDIn(orderIDs...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	type agg struct {
		qty, revenue int64
	}
	m := map[uint64]*agg{}
	for _, it := range items {
		a := m[it.ProductID]
		if a == nil {
			a = &agg{}
			m[it.ProductID] = a
		}
		a.qty += int64(it.Quantity)
		a.revenue += it.Amount
	}
	// 取 Top5 by revenue
	top := make([]TopProduct, 0, 5)
	for pid, a := range m {
		top = append(top, TopProduct{ProductID: pid, SoldQty: a.qty, Revenue: a.revenue})
	}
	for i := 0; i < len(top); i++ {
		for j := i + 1; j < len(top); j++ {
			if top[j].Revenue > top[i].Revenue {
				top[i], top[j] = top[j], top[i]
			}
		}
	}
	if len(top) > 5 {
		top = top[:5]
	}
	// 回填商品名
	for i := range top {
		if p, err := client.Product.Get(ctx, top[i].ProductID); err == nil {
			top[i].Name = p.Name
		}
	}
	return top, nil
}

var _ = ent.Asc // 保持引用
