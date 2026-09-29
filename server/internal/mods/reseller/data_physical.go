package reseller

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/resellerledgerentry"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

func (s *SettleService) reconcilePhysical(ctx context.Context, id uint64) error {
	return data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		o, e := data.LockFinancialOrder(ctx, s.repo.data, id)
		if e != nil {
			return e
		}
		if !o.ProfitEligible || o.SubsiteProfit <= 0 {
			return nil
		}
		c := data.Client(ctx, s.repo.data)
		if o.Extra["physical_markup_settled"] != true {
			return nil
		}
		_, _, target, e := data.PhysicalRefundTotals(ctx, s.repo.data, id)
		if e != nil {
			return e
		}
		delta := target - data.SnapshotInt(o.Extra["physical_markup_reversed"])
		if delta <= 0 {
			return nil
		}
		// Reverse unconfirmed profit first; otherwise it could become withdrawable
		// while a separate refund debt remains outstanding.
		pending, e := c.ResellerLedgerEntry.Query().Where(resellerledgerentry.OrderID(id), resellerledgerentry.SubsiteID(o.SubsiteID), resellerledgerentry.TypeEQ(resellerledgerentry.TypeOrderProfit), resellerledgerentry.StatusEQ(resellerledgerentry.StatusPending), resellerledgerentry.AmountGT(0)).All(ctx)
		if e != nil {
			return e
		}
		for _, row := range pending {
			take := min(delta, row.Amount)
			if take <= 0 {
				break
			}
			n, e := c.ResellerLedgerEntry.Update().Where(resellerledgerentry.ID(row.ID), resellerledgerentry.StatusEQ(resellerledgerentry.StatusPending), resellerledgerentry.Amount(row.Amount)).AddAmount(-take).Save(ctx)
			if e != nil {
				return e
			}
			if n != 1 {
				return fmt.Errorf("分站利润状态已变化，请重试")
			}
			if e = s.repo.updateBalance(ctx, o.SubsiteID, -take, 0, 0); e != nil {
				return e
			}
			if e = c.ResellerLedgerEntry.Create().SetSubsiteID(o.SubsiteID).SetOrderID(id).SetType("refund_deduct").SetAmount(-take).SetStatus(resellerledgerentry.StatusWithdrawn).SetIdempotencyKey(fmt.Sprintf("physical_markup_pending:%d:%d:%d", id, row.ID, target)).SetRemark("未确认商品利润退款扣回").Exec(ctx); e != nil {
				return e
			}
			delta -= take
		}
		if e = s.repo.RefundDeduct(ctx, o.SubsiteID, id, delta, 10000, fmt.Sprintf("physical_markup:%d:%d", id, target)); e != nil {
			return e
		}
		o.Extra["physical_markup_reversed"] = target
		return c.Order.UpdateOneID(id).SetExtra(o.Extra).Exec(ctx)
	})
}

func (s *SettleService) OnOrderPaid(ctx context.Context, env events.Envelope) error {
	var p settlePaidPayload
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
		if o.Extra["physical_markup_settled"] != true {
			if e = s.onOrderPaid(ctx, env); e != nil {
				return e
			}
			o.Extra["physical_markup_settled"] = true
			if e = data.Client(ctx, s.repo.data).Order.UpdateOneID(o.ID).SetExtra(o.Extra).Exec(ctx); e != nil {
				return e
			}
		}
		return s.reconcilePhysical(ctx, o.ID)
	})
}
