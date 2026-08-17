package dashboard

// AdminDashboardService 工作台服务（M1b v1）。

import (
	"context"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	affiliateport "github.com/NovaWorks/zcard-next/server/internal/mods/affiliate/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminDashboardService 服务。
type AdminDashboardService struct {
	adminv1.UnimplementedAdminDashboardServiceServer
	repo       *DashboardRepoImpl
	commission affiliateport.CommissionReader // P3-03 佣金列表（通道 A）
}

// NewAdminDashboardService 构造。
func NewAdminDashboardService(repo *DashboardRepoImpl, commission affiliateport.CommissionReader) *AdminDashboardService {
	return &AdminDashboardService{repo: repo, commission: commission}
}

// ListCommissions 佣金列表（P3-03；port 消费——跨模块零 ent 依赖）。
func (s *AdminDashboardService) ListCommissions(ctx context.Context, req *adminv1.ListCommissionsRequest) (*adminv1.ListCommissionsReply, error) {
	page := int(req.GetPage())
	if page < 1 {
		page = 1
	}
	size := int(req.GetPageSize())
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	rows, total, err := s.commission.ListCommissions(ctx, req.GetStatus(), page, size)
	if err != nil {
		return nil, errors.InternalServer("dashboard.QUERY_FAILED", err.Error())
	}
	reply := &adminv1.ListCommissionsReply{Total: total, Page: int32(page), PageSize: int32(size)}
	for _, c := range rows {
		reply.Commissions = append(reply.Commissions, &adminv1.AdminCommission{
			Id: c.ID, OrderId: c.OrderID, BuyerId: c.BuyerID, ReferrerId: c.ReferrerID,
			Tier: c.Tier, Rate: c.Rate, BaseAmount: c.BaseAmount, Amount: c.Amount,
			Status: c.Status, AvailableAt: c.AvailableAt, CreatedAt: c.CreatedAt,
		})
	}
	return reply, nil
}

// GetDashboard 工作台指标。
func (s *AdminDashboardService) GetDashboard(ctx context.Context, _ *emptypb.Empty) (*adminv1.DashboardReply, error) {
	today, last7d, last30d, err := s.repo.GetOverview(ctx)
	if err != nil {
		return nil, errors.InternalServer("dashboard.QUERY_FAILED", "统计失败: "+err.Error())
	}
	trend, err := s.repo.GetTrend(ctx)
	if err != nil {
		return nil, errors.InternalServer("dashboard.QUERY_FAILED", "趋势失败: "+err.Error())
	}
	top, err := s.repo.GetTopProducts(ctx)
	if err != nil {
		return nil, errors.InternalServer("dashboard.QUERY_FAILED", "排行失败: "+err.Error())
	}

	reply := &adminv1.DashboardReply{
		Today:   &adminv1.DashboardStat{Orders: today.Orders, Revenue: today.Revenue, PaidOrders: today.PaidOrders},
		Last7D:  &adminv1.DashboardStat{Orders: last7d.Orders, Revenue: last7d.Revenue, PaidOrders: last7d.PaidOrders},
		Last30D: &adminv1.DashboardStat{Orders: last30d.Orders, Revenue: last30d.Revenue, PaidOrders: last30d.PaidOrders},
	}
	for _, tp := range trend {
		reply.Trend = append(reply.Trend, &adminv1.DashboardTrendPoint{Date: tp.Date, Orders: tp.Orders, Revenue: tp.Revenue})
	}
	for _, p := range top {
		reply.TopProducts = append(reply.TopProducts, &adminv1.DashboardTopProduct{
			ProductId: p.ProductID, Name: p.Name, SoldQty: p.SoldQty, Revenue: p.Revenue,
		})
	}
	return reply, nil
}

// GetDailyStats 历史日结（分站视角自动隔离：subsite 取自 tenancy 上下文）。
func (s *AdminDashboardService) GetDailyStats(ctx context.Context, req *adminv1.GetDailyStatsRequest) (*adminv1.GetDailyStatsReply, error) {
	subsite := tenancy.FromContext(ctx).SubsiteID
	start, end := req.GetStartDate(), req.GetEndDate()
	if start == "" || end == "" {
		now := time.Now().UTC()
		start = now.AddDate(0, 0, -6).Format("20060102")
		end = now.Format("20060102")
	}
	points, err := s.repo.GetDailyStats(ctx, subsite, start, end)
	if err != nil {
		return nil, errors.InternalServer("dashboard.DAILY_FAILED", "读取日结失败")
	}
	reply := &adminv1.GetDailyStatsReply{}
	for _, p := range points {
		reply.Points = append(reply.Points, &adminv1.DailyStatPoint{
			Date: p.Date, Orders: p.Orders, AmountCents: p.Amount, PaidOrders: p.Paid,
		})
	}
	return reply, nil
}

// GetReconciliation 对账总览（P3-07）。
func (s *AdminDashboardService) GetReconciliation(ctx context.Context, req *adminv1.GetReconciliationRequest) (*adminv1.GetReconciliationReply, error) {
	sum, err := s.repo.GetReconciliation(ctx, req.GetDate())
	if err != nil {
		return nil, errors.InternalServer("dashboard.QUERY_FAILED", err.Error())
	}
	return &adminv1.GetReconciliationReply{
		Summary: &adminv1.ReconciliationSummary{
			Date:                sum.Date,
			OrderPaidTotal:      sum.OrderPaidTotal,
			PaymentSuccessTotal: sum.PaymentSuccessTotal,
			WalletRechargeTotal: sum.WalletRechargeTotal,
			CommissionTotal:     sum.CommissionTotal,
			OrderCount:          sum.OrderCount,
			MismatchCount:       sum.MismatchCount,
		},
	}, nil
}
