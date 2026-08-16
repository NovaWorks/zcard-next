package dashboard

// AdminDashboardService 工作台服务（M1b v1）。

import (
	"context"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminDashboardService 服务。
type AdminDashboardService struct {
	adminv1.UnimplementedAdminDashboardServiceServer
	repo *DashboardRepoImpl
}

// NewAdminDashboardService 构造。
func NewAdminDashboardService(repo *DashboardRepoImpl) *AdminDashboardService {
	return &AdminDashboardService{repo: repo}
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
