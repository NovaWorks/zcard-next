package affiliate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/affiliatecommission"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

func (s *AffiliateService) OnOrderPaid(ctx context.Context, env events.Envelope) error {
	var p paidPayload
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
		return s.reconcilePhysical(ctx, o)
	})
}
func (s *AffiliateService) reconcilePhysical(ctx context.Context, o *ent.Order) error {
	c := data.Client(ctx, s.repo.data)
	goods, refunded, _, e := data.PhysicalRefundTotals(ctx, s.repo.data, o.ID)
	if e != nil {
		return e
	}
	if refunded == 0 {
		return nil
	}
	rows, e := c.AffiliateCommission.Query().Where(affiliatecommission.OrderIDIn(o.ID, o.ID+1_000_000_000_000), affiliatecommission.TierGT(0)).Order(ent.Asc(affiliatecommission.FieldID)).All(ctx)
	if e != nil {
		return e
	}
	receipt, e := c.RefundOrder.Query().Where(refundorder.OrderID(o.ID), refundorder.StatusEQ(refundorder.StatusSucceeded)).Order(ent.Desc(refundorder.FieldID)).First(ctx)
	if e != nil {
		return e
	}
	for tier := int8(1); tier <= 3; tier++ {
		var original int64
		var referrer uint64
		for _, row := range rows {
			if row.Tier == tier {
				original = max(original, data.Prorate(row.BaseAmount, int64(row.Rate), 10000))
				referrer = row.ReferrerID
			}
		}
		key := fmt.Sprintf("physical_commission_reversed_%d", tier)
		target := data.Prorate(original, refunded, goods)
		delta := target - data.SnapshotInt(o.Extra[key])
		if delta <= 0 {
			continue
		}
		var debit int64
		remaining := delta
		for _, row := range rows {
			if row.Tier != tier || row.Amount <= 0 || row.Status == affiliatecommission.StatusReversed {
				continue
			}
			take := min(remaining, row.Amount)
			if take <= 0 {
				continue
			}
			q := c.AffiliateCommission.Update().Where(affiliatecommission.ID(row.ID), affiliatecommission.StatusEQ(row.Status), affiliatecommission.Amount(row.Amount)).SetAmount(row.Amount - take)
			if row.Amount == take {
				q.SetStatus(affiliatecommission.StatusReversed)
			}
			n, err := q.Save(ctx)
			if err != nil {
				return err
			}
			if n != 1 {
				return fmt.Errorf("佣金已变化，请重试退款扣回")
			}
			if row.Status != affiliatecommission.StatusPendingConfirm {
				debit += take
			}
			remaining -= take
		}
		if remaining != 0 {
			return fmt.Errorf("订单佣金分摊余额异常")
		}
		if debit > 0 {
			e = s.wallet.DebitInTx(ctx, walletport.Entry{UserID: referrer, Direction: "out", Type: "commission_reversal", Amount: centsOf(debit), Reference: fmt.Sprintf("physical_commission:%d:%d:%d", o.ID, tier, target), Remark: "商品退款佣金扣回"})
			if e != nil {
				if !errors.Is(e, walletport.ErrInsufficientBalance) {
					return e
				}
				if e = s.repo.InsertDebt(ctx, 3_000_000_000_000+receipt.ID, referrer, tier, debit); e != nil {
					return e
				}
			}
		}
		o.Extra[key] = target
	}
	return c.Order.UpdateOneID(o.ID).SetExtra(o.Extra).Exec(ctx)
}
func (s *AffiliateService) physicalRefund(ctx context.Context, id uint64) error {
	return data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		o, e := data.LockFinancialOrder(ctx, s.repo.data, id)
		if e != nil {
			return e
		}
		return s.reconcilePhysical(ctx, o)
	})
}

func (s *AffiliateService) confirmOne(ctx context.Context, id uint64) error {
	return data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		client := data.Client(ctx, s.repo.data)
		row, e := client.AffiliateCommission.Get(ctx, id)
		if e != nil {
			return e
		}
		o, e := client.Order.Get(ctx, row.OrderID)
		if e != nil && !ent.IsNotFound(e) {
			return e
		}
		if o != nil && o.CommerceVersion == 1 {
			if _, e = data.LockFinancialOrder(ctx, s.repo.data, o.ID); e != nil {
				return e
			}
		}
		row, e = client.AffiliateCommission.Get(ctx, id)
		if e != nil {
			return e
		}
		if row.Status != affiliatecommission.StatusPendingConfirm {
			return nil
		}
		n, e := client.AffiliateCommission.Update().Where(affiliatecommission.ID(id), affiliatecommission.StatusEQ(affiliatecommission.StatusPendingConfirm), affiliatecommission.Amount(row.Amount)).SetStatus(affiliatecommission.StatusAvailable).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return fmt.Errorf("佣金状态已变化")
		}
		if row.Amount > 0 {
			return s.wallet.CreditInTx(ctx, walletport.Entry{UserID: row.ReferrerID, Direction: "in", Type: "commission", Amount: centsOf(row.Amount), Reference: refKey(id), Remark: "佣金到期确认"})
		}
		if row.Amount < 0 {
			return s.wallet.DebitInTx(ctx, walletport.Entry{UserID: row.ReferrerID, Direction: "out", Type: "commission_debt", Amount: centsOf(-row.Amount), Reference: debtRefKey(id), Remark: "佣金负债抵扣"})
		}
		return nil
	})
}
