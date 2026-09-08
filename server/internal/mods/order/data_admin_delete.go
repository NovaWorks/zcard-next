package order

import (
	"context"
	"strings"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderstatusevent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// DeleteOrders 只从管理列表移除无用订单，支付回调、对账和幂等查询保留原记录。
// 整批事务提交，状态、付款或租户校验失败时不删除任何一条。
func (s *AdminOrderService) DeleteOrders(ctx context.Context, req *adminv1.DeleteOrdersRequest) (*emptypb.Empty, error) {
	if len(req.GetOrderNos()) == 0 || len(req.GetOrderNos()) > 100 {
		return nil, errors.BadRequest("order.INVALID_SELECTION", "每次请选择 1 至 100 条订单")
	}
	codes := make([]string, 0, len(req.GetOrderNos()))
	seen := make(map[string]bool)
	for _, code := range req.GetOrderNos() {
		code = strings.TrimSpace(code)
		if code == "" {
			return nil, errors.BadRequest("order.INVALID_SELECTION", "订单号不能为空")
		}
		if !seen[code] {
			seen[code] = true
			codes = append(codes, code)
		}
	}
	subsiteID := tenancy.FromContext(ctx).SubsiteID
	err := data.Tx(ctx, s.data, func(ctx context.Context) error {
		client := data.Client(ctx, s.data)
		rows, err := client.Order.Query().Where(order.SubsiteID(subsiteID), order.OrderNoIn(codes...)).All(ctx)
		if err != nil {
			return err
		}
		if len(rows) != len(codes) {
			return errors.NotFound("order.NOT_FOUND", "部分订单不存在，请刷新列表后重试")
		}
		for _, row := range rows {
			if row.AdminDeletedAt != nil {
				continue // 重复请求幂等，不重复创建删除事件。
			}
			n, err := client.Order.Update().Where(
				order.ID(row.ID), order.SubsiteID(subsiteID), order.Version(row.Version),
				order.AdminDeletedAtIsNil(), order.PaidAtIsNil(),
				order.StatusIn(order.StatusCanceled, order.StatusExpired),
				order.Not(order.HasPaymentsWith(payment.StatusEQ(payment.StatusSuccess))),
				order.Not(order.HasDeliveries()), order.Not(order.HasRefunds()),
			).SetAdminDeletedAt(time.Now().UTC()).AddVersion(1).Save(ctx)
			if err != nil {
				return err
			}
			if n != 1 {
				return errors.BadRequest("order.CANNOT_DELETE", "仅可删除已取消或已过期且无付款、发货、退款记录的订单；请刷新后重试")
			}
			if _, err := client.OrderStatusEvent.Create().SetOrderID(row.ID).
				SetFromStatus(string(row.Status)).SetToStatus(string(row.Status)).
				SetEvent("admin_deleted").SetOperator(orderstatusevent.OperatorAdmin).
				SetReason("管理员从订单列表删除无用订单，保留关联记录").Save(ctx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}
