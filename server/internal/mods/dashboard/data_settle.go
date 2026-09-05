package dashboard

// 日结任务：daily_stats 落表（报表只扫此表不扫大表）。
// 幂等：唯一索引 (subsite_id, stat_date, metric, dimension_key) 重跑覆盖。
// 调度：每小时检查 + 当日标记（00:10–01:00 窗口跑昨日聚合，防漏跑/重复跑）。

import (
	"context"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/dailystat"
)

// settleMetrics 日结指标集（dimension_key 空=总量行）。
var settleMetrics = []string{"orders", "amount", "paid_orders"}

// RunDailySettle 聚合指定日各租户指标并落 daily_stats（重跑覆盖同维度行）。
func (r *DashboardRepoImpl) RunDailySettle(ctx context.Context, day time.Time) error {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	date := start.Format("20060102")
	client := data.Client(ctx, r.data)

	// 全部租户（主站 0 + 订单表中出现过的分站）
	subsites := []uint64{0}
	rows, err := client.Order.Query().All(ctx)
	if err != nil {
		return err
	}
	seen := map[uint64]bool{0: true}
	for _, o := range rows {
		if !seen[o.SubsiteID] {
			seen[o.SubsiteID] = true
			subsites = append(subsites, o.SubsiteID)
		}
	}

	for _, subsite := range subsites {
		m, err := r.metricBetweenSubsite(ctx, subsite, start, end)
		if err != nil {
			return err
		}
		values := map[string]int64{
			"orders": m.Orders, "amount": m.Revenue, "paid_orders": m.PaidOrders,
		}
		for _, metric := range settleMetrics {
			if err := r.upsertDailyStat(ctx, subsite, date, metric, values[metric]); err != nil {
				return err
			}
		}
	}
	return nil
}

// upsertDailyStat 唯一索引幂等：存在更新、不存在创建（重跑覆盖）。
func (r *DashboardRepoImpl) upsertDailyStat(ctx context.Context, subsite uint64, date, metric string, value int64) error {
	client := data.Client(ctx, r.data)
	existing, err := client.DailyStat.Query().
		Where(
			dailystat.SubsiteIDEQ(subsite),
			dailystat.StatDateEQ(date),
			dailystat.MetricEQ(metric),
			dailystat.DimensionKeyEQ(""),
		).Only(ctx)
	if err == nil {
		_, err = client.DailyStat.UpdateOneID(existing.ID).SetValue(value).Save(ctx)
		return err
	}
	if !isNotFound(err) {
		return err
	}
	_, err = client.DailyStat.Create().
		SetSubsiteID(subsite).
		SetStatDate(date).
		SetMetric(metric).
		SetDimensionKey("").
		SetValue(value).
		Save(ctx)
	return err
}

// DailyStatPoint 日结查询点。
type DailyStatPoint struct {
	Date   string
	Orders int64
	Amount int64
	Paid   int64
}

// GetDailyStats 历史日结查询（只扫 daily_stats，不扫大表）。
func (r *DashboardRepoImpl) GetDailyStats(ctx context.Context, subsiteID uint64, startDate, endDate string) ([]DailyStatPoint, error) {
	rows, err := data.Client(ctx, r.data).DailyStat.Query().
		Where(
			dailystat.SubsiteIDEQ(subsiteID),
			dailystat.StatDateGTE(startDate),
			dailystat.StatDateLTE(endDate),
		).
		Order(dailystat.ByStatDate()).
		All(ctx)
	if err != nil {
		return nil, err
	}
	byDate := map[string]*DailyStatPoint{}
	var order []string
	for _, row := range rows {
		p, ok := byDate[row.StatDate]
		if !ok {
			p = &DailyStatPoint{Date: row.StatDate}
			byDate[row.StatDate] = p
			order = append(order, row.StatDate)
		}
		switch row.Metric {
		case "orders":
			p.Orders = row.Value
		case "amount":
			p.Amount = row.Value
		case "paid_orders":
			p.Paid = row.Value
		}
	}
	out := make([]DailyStatPoint, 0, len(order))
	for _, d := range order {
		out = append(out, *byDate[d])
	}
	return out, nil
}

// DailySettleCron 日结调度（每小时检查；00:10–01:00 窗口跑昨日，当日标记防重跑）。
func (r *DashboardRepoImpl) DailySettleCron() func(context.Context) {
	lastRun := ""
	return func(ctx context.Context) {
		now := time.Now()
		hour := now.Hour()
		if hour != 0 && hour != 1 {
			return // 仅 00–01 点窗口
		}
		today := now.Format("20060102")
		if lastRun == today {
			return
		}
		yesterday := now.AddDate(0, 0, -1)
		if err := r.RunDailySettle(ctx, yesterday); err != nil {
			return // 失败下轮重试（lastRun 不更新）
		}
		lastRun = today
	}
}

func isNotFound(err error) bool {
	return ent.IsNotFound(err)
}
