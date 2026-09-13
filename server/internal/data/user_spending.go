package data

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
)

// UserSpending is the shared member/admin spending total, in cents. Fully refunded,
// unpaid, canceled and expired orders do not count; completed orders still count.
func UserSpending(ctx context.Context, client *ent.Client, userIDs []uint64) (map[uint64]int64, error) {
	result := make(map[uint64]int64, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	var rows []struct {
		UserID uint64 `json:"user_id"`
		Sum    int64  `json:"sum"`
	}
	err := client.Order.Query().Where(order.UserIDIn(userIDs...), order.UserIDGT(0), order.StatusIn(
		order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered, order.StatusDelivered, order.StatusCompleted, order.StatusRefundPending,
	)).GroupBy(order.FieldUserID).Aggregate(ent.Sum(order.FieldTotalAmount)).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		result[r.UserID] = r.Sum
	}
	return result, nil
}
