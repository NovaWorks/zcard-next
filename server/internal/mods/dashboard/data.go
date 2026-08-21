package dashboard

// 工作台指标聚合（M1b v1）：今日/近7天/近30天订单数与营收 + 趋势 + 商品 Top5。
// 金额口径：已支付订单（status 非 pending_payment/canceled/expired）的 total_amount 求和（分）。

import (
	"context"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/affiliatecommission"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/wallettransaction"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/withdrawal"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// Metric 统计项。
type Metric struct {
	Orders     int64
	Revenue    int64
	PaidOrders int64
	Cost       int64
	Profit     int64
	NewUsers   int64
}

// TrendPoint 趋势点。
type TrendPoint struct {
	Date      string
	Orders    int64
	Revenue   int64
	PaidCount int64
	Cost      int64
	Profit    int64
}

// TopProduct 商品排行。
type TopProduct struct {
	ProductID uint64
	Name      string
	SoldQty   int64
	Revenue   int64
}

// TopChannel 支付渠道排行。
type TopChannel struct {
	Channel      string
	TotalCount   int64
	SuccessCount int64
	FailedCount  int64
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

// GetOverview 返回 6 个统计窗口：today/yesterday/last7d/prev7d/last30d/prev30d
// （后三者为环比基准；P3-07 M3：分站视角自动隔离——按 tenancy.Context.SubsiteID
// 过滤，分站后台只看本站；new_users 为全局注册用户，用户表不分站）。
func (r *DashboardRepoImpl) GetOverview(ctx context.Context) (today, yesterday, last7d, prev7d, last30d, prev30d Metric, err error) {
	subsite := tenancy.FromContext(ctx).SubsiteID
	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if m, e := r.metricBetween(ctx, subsite, todayStart, now); e == nil {
		today = m
	}
	if m, e := r.metricBetween(ctx, subsite, todayStart.AddDate(0, 0, -1), todayStart); e == nil {
		yesterday = m
	}
	if m, e := r.metricBetween(ctx, subsite, now.AddDate(0, 0, -7), now); e == nil {
		last7d = m
	}
	if m, e := r.metricBetween(ctx, subsite, now.AddDate(0, 0, -14), now.AddDate(0, 0, -7)); e == nil {
		prev7d = m
	}
	if m, e := r.metricBetween(ctx, subsite, now.AddDate(0, 0, -30), now); e == nil {
		last30d = m
	}
	if m, e := r.metricBetween(ctx, subsite, now.AddDate(0, 0, -60), now.AddDate(0, 0, -30)); e == nil {
		prev30d = m
	}
	return today, yesterday, last7d, prev7d, last30d, prev30d, nil
}

// metricBetweenSubsite 指定租户区段聚合（日结任务用）。
func (r *DashboardRepoImpl) metricBetweenSubsite(ctx context.Context, subsite uint64, start, end time.Time) (Metric, error) {
	client := data.Client(ctx, r.data)
	orders, err := client.Order.Query().
		Where(
			order.CreatedAtGTE(start),
			order.CreatedAtLTE(end),
			order.SubsiteID(subsite),
		).
		All(ctx)
	if err != nil {
		return Metric{}, err
	}
	m := Metric{Orders: int64(len(orders))}
	paid := map[order.Status]bool{}
	for _, st := range paidStatuses() {
		paid[st] = true
	}
	for _, o := range orders {
		if paid[o.Status] {
			m.PaidOrders++
			m.Revenue += o.TotalAmount
			m.Cost += o.Cost
		}
	}
	m.Profit = m.Revenue - m.Cost
	// 新增注册用户（全局表不分站；失败不阻断主统计）
	if n, e := client.User.Query().Where(user.CreatedAtGTE(start), user.CreatedAtLTE(end)).Count(ctx); e == nil {
		m.NewUsers = int64(n)
	}
	return m, nil
}

func (r *DashboardRepoImpl) metricBetween(ctx context.Context, subsite uint64, start, end time.Time) (Metric, error) {
	return r.metricBetweenSubsite(ctx, subsite, start, end)
}

// GetTrend 近 N 天每日订单数/已支付数/营收/成本/利润（含今日，共 N 个桶；分站隔离）。
// days 支持 7/14/30，非法值回落 7。
func (r *DashboardRepoImpl) GetTrend(ctx context.Context, days int) ([]TrendPoint, error) {
	if days != 14 && days != 30 {
		days = 7
	}
	subsite := tenancy.FromContext(ctx).SubsiteID
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -(days - 1))
	client := data.Client(ctx, r.data)
	rows, err := client.Order.Query().
		Where(order.CreatedAtGTE(start), order.SubsiteID(subsite)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	paid := map[order.Status]bool{}
	for _, s := range paidStatuses() {
		paid[s] = true
	}
	// 初始化 N 天桶
	buckets := map[string]*TrendPoint{}
	for i := 0; i < days; i++ {
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
			bp.PaidCount++
			bp.Revenue += o.TotalAmount
			bp.Cost += o.Cost
		}
	}
	out := make([]TrendPoint, 0, days)
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		bp := buckets[d]
		bp.Profit = bp.Revenue - bp.Cost
		out = append(out, *bp)
	}
	return out, nil
}

// GetTopChannels 近 30 天支付渠道排行（分站隔离；按 channel 分组计数）。
func (r *DashboardRepoImpl) GetTopChannels(ctx context.Context) ([]TopChannel, error) {
	subsite := tenancy.FromContext(ctx).SubsiteID
	start := time.Now().UTC().AddDate(0, 0, -30)
	client := data.Client(ctx, r.data)
	rows, err := client.Payment.Query().
		Where(payment.CreatedAtGTE(start), payment.SubsiteID(subsite)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	m := map[string]*TopChannel{}
	for _, p := range rows {
		c := m[p.Channel]
		if c == nil {
			c = &TopChannel{Channel: p.Channel}
			m[p.Channel] = c
		}
		c.TotalCount++
		switch p.Status {
		case payment.StatusSuccess:
			c.SuccessCount++
		case payment.StatusFailed:
			c.FailedCount++
		}
	}
	out := make([]TopChannel, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].TotalCount > out[i].TotalCount {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// GetLowStockCount 库存预警商品数：上架商品（status=1）中可用卡密数 < threshold。
func (r *DashboardRepoImpl) GetLowStockCount(ctx context.Context, threshold int) (int64, error) {
	if threshold < 1 {
		threshold = 10
	}
	subsite := tenancy.FromContext(ctx).SubsiteID
	client := data.Client(ctx, r.data)
	// 按商品聚合可用卡密数（ent Scan 列映射：字段名小写化 + sql/json tag——见 ent scan.go）
	var stock []struct {
		ProductID uint64 `json:"product_id"`
		Count     int    `json:"count"`
	}
	if err := client.Card.Query().
		Where(card.StatusEQ(card.StatusAvailable), card.SubsiteID(subsite)).
		GroupBy(card.FieldProductID).
		Aggregate(ent.Count()).
		Scan(ctx, &stock); err != nil {
		return 0, err
	}
	counts := make(map[uint64]int, len(stock))
	for _, s := range stock {
		counts[s.ProductID] = s.Count
	}
	products, err := client.Product.Query().
		Where(product.StatusEQ(1), product.SubsiteID(subsite)).
		All(ctx)
	if err != nil {
		return 0, err
	}
	var low int64
	for _, p := range products {
		if counts[p.ID] < threshold {
			low++
		}
	}
	return low, nil
}

// GetPending 待办统计：待审核提现（全局）、待处理退款、履约中订单（分站）。
func (r *DashboardRepoImpl) GetPending(ctx context.Context) (withdrawals, refunds, fulfilling int64, err error) {
	subsite := tenancy.FromContext(ctx).SubsiteID
	client := data.Client(ctx, r.data)
	if n, e := client.Withdrawal.Query().Where(withdrawal.StatusEQ(withdrawal.StatusPending)).Count(ctx); e == nil {
		withdrawals = int64(n)
	}
	if n, e := client.Order.Query().
		Where(order.StatusEQ(order.StatusRefundPending), order.SubsiteID(subsite)).
		Count(ctx); e == nil {
		refunds = int64(n)
	}
	if n, e := client.Order.Query().
		Where(order.StatusEQ(order.StatusFulfilling), order.SubsiteID(subsite)).
		Count(ctx); e == nil {
		fulfilling = int64(n)
	}
	return withdrawals, refunds, fulfilling, nil
}

// GetTopProducts 近 30 天销量 Top5（分站隔离）。
func (r *DashboardRepoImpl) GetTopProducts(ctx context.Context) ([]TopProduct, error) {
	subsite := tenancy.FromContext(ctx).SubsiteID
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -30)
	client := data.Client(ctx, r.data)
	paidOrders, err := client.Order.Query().
		Where(order.CreatedAtGTE(start), order.StatusIn(paidStatuses()...), order.SubsiteID(subsite)).
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

// ReconciliationSummary 对账汇总（P3-07：订单×支付×充值×佣金四向基础核对）。
type ReconciliationSummary struct {
	Date                string
	OrderPaidTotal      int64
	PaymentSuccessTotal int64
	WalletRechargeTotal int64
	CommissionTotal     int64
	OrderCount          int64
	MismatchCount       int64
}

// GetReconciliation 当日对账（口径：本地时区日界——运营对账口径；金额一律分）。
func (r *DashboardRepoImpl) GetReconciliation(ctx context.Context, date string) (*ReconciliationSummary, error) {
	var day time.Time
	if d, err := time.ParseInLocation("20060102", date, time.Local); err == nil {
		day = d
	} else {
		now := time.Now()
		day = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	}
	start := day.UTC()
	end := day.AddDate(0, 0, 1).UTC()

	client := data.Client(ctx, r.data)
	out := &ReconciliationSummary{Date: day.Format("20060102")}

	// 1) 当日已支付订单
	orders, err := client.Order.Query().
		Where(order.PaidAtGTE(start), order.PaidAtLT(end)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, o := range orders {
		out.OrderPaidTotal += o.TotalAmount
		out.OrderCount++
	}

	// 2) 当日支付单成功额
	pays, err := client.Payment.Query().
		Where(payment.PaidAtGTE(start), payment.PaidAtLT(end)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range pays {
		out.PaymentSuccessTotal += p.ChargedAmount
	}

	// 3) 当日钱包充值（direction=in & type=recharge）
	recharges, err := client.WalletTransaction.Query().
		Where(
			wallettransaction.CreatedAtGTE(start),
			wallettransaction.CreatedAtLT(end),
			wallettransaction.DirectionEQ("in"),
			wallettransaction.TypeEQ("recharge"),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, w := range recharges {
		out.WalletRechargeTotal += w.Amount
	}

	// 4) 当日佣金计提（正佣金）
	commissions, err := client.AffiliateCommission.Query().
		Where(
			affiliatecommission.CreatedAtGTE(start),
			affiliatecommission.CreatedAtLT(end),
			affiliatecommission.AmountGT(0),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range commissions {
		out.CommissionTotal += c.Amount
	}

	// 5) 差异：订单额 vs 支付单额（余额支付订单会天然差异——差额 = 余额支付部分 + 手续费；
	// 报表层只报差异数，人工判读）
	diff := out.OrderPaidTotal - out.PaymentSuccessTotal - out.WalletRechargeTotal
	if diff < 0 {
		diff = -diff
	}
	if diff > 0 {
		out.MismatchCount = 1 // 汇总级差异标记（明细级对账 M4 reconciliation_jobs）
	}
	return out, nil
}
