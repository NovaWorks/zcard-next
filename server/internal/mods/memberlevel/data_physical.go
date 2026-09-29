package memberlevel

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pointtransaction"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

func (s *PointsService) OnOrderPaid(ctx context.Context, env events.Envelope) error {
	var p orderPaidPointsPayload
	if e := json.Unmarshal(env.Payload, &p); e != nil {
		return e
	}
	o, e := data.Client(ctx, s.repo.data).Order.Get(ctx, p.OrderID)
	if ent.IsNotFound(e) {
		return s.onOrderPaid(ctx, env)
	}
	if e != nil {
		return e
	}
	if o.CommerceVersion != 1 {
		return s.onOrderPaid(ctx, env)
	}
	return data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		o, e := data.LockFinancialOrder(ctx, s.repo.data, p.OrderID)
		if e != nil {
			return e
		}
		if e = s.onOrderPaid(ctx, env); e != nil {
			return e
		}
		return s.reconcilePhysicalPoints(ctx, o)
	})
}
func (s *PointsService) OnOrderRefunded(ctx context.Context, env events.Envelope) error {
	var p struct {
		OrderID         uint64 `json:"order_id"`
		CommerceVersion int32  `json:"commerce_version"`
	}
	if e := json.Unmarshal(env.Payload, &p); e != nil {
		return e
	}
	if p.CommerceVersion != 1 {
		return nil
	}
	return data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		o, e := data.LockFinancialOrder(ctx, s.repo.data, p.OrderID)
		if e != nil {
			return e
		}
		return s.reconcilePhysicalPoints(ctx, o)
	})
}
func (s *PointsService) reconcilePhysicalPoints(ctx context.Context, o *ent.Order) error {
	c := data.Client(ctx, s.repo.data)
	earned, e := c.PointTransaction.Query().Where(pointtransaction.Reference(fmt.Sprintf("points:%d", o.ID))).Only(ctx)
	if ent.IsNotFound(e) {
		return nil
	}
	if e != nil {
		return e
	}
	goods, refunded, _, e := data.PhysicalRefundTotals(ctx, s.repo.data, o.ID)
	if e != nil {
		return e
	}
	target := data.Prorate(earned.Amount, refunded, goods)
	delta := target - data.SnapshotInt(o.Extra["physical_points_reversed"])
	if delta <= 0 {
		return nil
	}
	reverser, ok := s.points.(interface {
		PointRevokeInTx(context.Context, walletport.PointEntry) error
	})
	if !ok {
		return fmt.Errorf("积分退款服务未装配")
	}
	if e = reverser.PointRevokeInTx(ctx, walletport.PointEntry{UserID: o.UserID, Direction: "out", Type: "refund_revoke", Amount: delta, Reference: fmt.Sprintf("points_refund:%d:%d", o.ID, target), OrderID: o.ID, Remark: "商品退款撤销积分，已使用部分形成积分欠额"}); e != nil {
		return e
	}
	o.Extra["physical_points_reversed"] = target
	return c.Order.UpdateOneID(o.ID).SetExtra(o.Extra).Exec(ctx)
}
