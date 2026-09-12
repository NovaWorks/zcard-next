package dashboard

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/ticket"
)

// GetTicketPending 与工单工作台一致：全站客服工单，已解决/关闭不参与提醒。
func (r *DashboardRepoImpl) GetTicketPending(ctx context.Context) (open, processing, urgent int64, err error) {
	var rows []struct {
		Status   string `json:"status"`
		Priority string `json:"priority"`
		Count    int64  `json:"count"`
	}
	err = data.Client(ctx, r.data).Ticket.Query().Where(ticket.StatusIn(ticket.StatusOpen, ticket.StatusProcessing)).
		GroupBy(ticket.FieldStatus, ticket.FieldPriority).Aggregate(ent.Count()).Scan(ctx, &rows)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, row := range rows {
		if row.Status == string(ticket.StatusOpen) {
			open += row.Count
		} else {
			processing += row.Count
		}
		if row.Priority == string(ticket.PriorityUrgentPaid) {
			urgent += row.Count
		}
	}
	return
}
