package lottery

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryaccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotterydraw"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryprize"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/securityauditlog"
	"strings"
	"time"
)

func (r *Repo) Draw(ctx context.Context, id, uid uint64, key string) (*ent.LotteryDraw, error) {
	if e := requestKey(key); e != nil {
		return nil, e
	}
	var result *ent.LotteryDraw
	e := data.Tx(ctx, r.Data, func(ctx context.Context) error {
		a, e := r.activity(ctx, id, true)
		if e != nil {
			return e
		}
		c := data.Client(ctx, r.Data)
		old, e := c.LotteryDraw.Query().Where(lotterydraw.SubsiteID(site(ctx)), lotterydraw.ActivityID(id), lotterydraw.UserID(uid), lotterydraw.RequestKey(key)).Only(ctx)
		if e == nil {
			result = old
			return nil
		}
		if !ent.IsNotFound(e) {
			return e
		}
		now := r.now()
		if e = active(a, now); e != nil {
			return e
		}
		ps, e := r.prizes(ctx, id)
		if e != nil {
			return e
		}
		if e = r.ready(ctx, ps); e != nil {
			return e
		}
		acc, e := r.account(ctx, a, uid, now)
		if e != nil {
			return e
		}
		if e = r.autoGrant(ctx, a, acc); e != nil {
			return e
		}
		if acc.Balance <= 0 {
			return bad("抽奖次数已用完")
		}
		n, e := c.LotteryAccount.Update().Where(lotteryaccount.ID(acc.ID), lotteryaccount.BalanceGT(0)).AddBalance(-1).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return bad("抽奖次数已用完")
		}
		roll, e := r.random()
		if e != nil {
			return e
		}
		if roll < 0 || roll >= 10000 {
			return bad("抽奖服务暂不可用")
		}
		var winner *ent.LotteryPrize
		for _, p := range ps {
			if roll < int(p.Probability) {
				winner = p
				break
			}
			roll -= int(p.Probability)
		}
		no, e := newNo()
		if e != nil {
			return e
		}
		build := c.LotteryDraw.Create().SetSubsiteID(site(ctx)).SetActivityID(id).SetUserID(uid).SetDrawNo(no).SetRequestKey(key).SetActivityName(a.Name).SetRevision(a.Revision).SetRuleSnapshot(snapshot(a, ps)).SetStatus("missed")
		if winner != nil {
			n, e := c.LotteryPrize.Update().Where(lotteryprize.ID(winner.ID), lotteryprize.IssuedLT(winner.Quantity)).AddIssued(1).Save(ctx)
			if e != nil {
				return e
			}
			if n != 1 {
				return bad("奖品已发完，本次未扣次数")
			}
			build = build.SetPrizeID(winner.ID).SetPrizeName(winner.Name).SetMode(winner.Mode).SetProductID(winner.ProductID).SetSkuID(winner.SkuID)
			var plain string
			if winner.Mode == "card" {
				p, e := r.eligibleCard(ctx, winner.ProductID, winner.SkuID)
				if e != nil {
					return e
				}
				q := r.cardQuery(ctx, winner.ProductID, winner.SkuID).Order(ent.Asc(card.FieldID)).Limit(1)
				if r.Data.Dialect.Capabilities().SupportsSkipLocked {
					q = q.ForUpdate()
				}
				ca, e := q.First(ctx)
				if ent.IsNotFound(e) {
					return bad("奖品库存不足，本次未扣次数")
				}
				if e != nil {
					return e
				}
				plain, e = r.Cipher.Open(ca.Content, ca.ProductID, ca.SubsiteID)
				if e != nil {
					return bad("奖品内容暂时无法读取，本次未扣次数，请联系平台")
				}
				// available→used 一次性条件写，不留下会被订单绑定/超时回收的 reserved 中间态。
				n, e = c.Card.Update().Where(card.ID(ca.ID), card.SubsiteID(site(ctx)), card.StatusEQ(card.StatusAvailable)).SetStatus(card.StatusUsed).SetUsedAt(now).Save(ctx)
				if e != nil {
					return e
				}
				if n != 1 {
					return bad("奖品库存已变化，本次未扣次数")
				}
				cost := p.FactoryPrice
				if winner.SkuID != 0 {
					sk, e := c.ProductSku.Query().Where(productsku.ID(winner.SkuID), productsku.ProductID(p.ID)).Only(ctx)
					if e != nil {
						return e
					}
					if sk.Cost > 0 {
						cost = sk.Cost
					}
				}
				build = build.SetCardID(ca.ID).SetCost(cost)
			} else {
				plain, e = r.Cipher.OpenLottery(winner.Content, fmt.Sprintf("prize:%d", winner.ID), site(ctx))
				if e != nil {
					return e
				}
			}
			sealed, e := r.Cipher.SealLottery(plain, "draw:"+no, site(ctx))
			if e != nil {
				return e
			}
			build = build.SetContent(sealed)
			if winner.Mode == "manual" {
				build = build.SetStatus("pending")
			} else {
				build = build.SetStatus("delivered").SetDeliveredAt(now)
			}
		}
		result, e = build.Save(ctx)
		if e != nil {
			return e
		}
		_, e = c.LotteryChanceLog.Create().SetSubsiteID(site(ctx)).SetActivityID(id).SetUserID(uid).SetPeriod(acc.Period).SetAmount(-1).SetKind("draw").SetRequestKey("draw:" + key).Save(ctx)
		return e
	})
	if e != nil {
		return nil, e
	}
	return result, nil
}
func (r *Repo) Result(ctx context.Context, no string, uid uint64) (*ent.LotteryDraw, error) {
	q := data.Client(ctx, r.Data).LotteryDraw.Query().Where(lotterydraw.SubsiteID(site(ctx)), lotterydraw.DrawNo(no))
	if uid != 0 {
		q = q.Where(lotterydraw.UserID(uid))
	}
	d, e := q.Only(ctx)
	if ent.IsNotFound(e) {
		return nil, bad("中奖记录不存在")
	}
	return d, e
}
func (r *Repo) Content(ctx context.Context, d *ent.LotteryDraw) (string, error) {
	if len(d.Content) == 0 {
		return "", nil
	}
	return r.Cipher.OpenLottery(d.Content, "draw:"+d.DrawNo, d.SubsiteID)
}
func (r *Repo) Deliver(ctx context.Context, no, remark string) error {
	if len([]rune(remark)) > 500 {
		return bad("备注最多 500 字")
	}
	n, e := data.Client(ctx, r.Data).LotteryDraw.Update().Where(lotterydraw.SubsiteID(site(ctx)), lotterydraw.DrawNo(no), lotterydraw.Mode("manual"), lotterydraw.Status("pending")).SetStatus("delivered").SetDeliveredAt(r.now()).SetAdminID(actor(ctx)).SetRemark(strings.TrimSpace(remark)).Save(ctx)
	if e != nil {
		return e
	}
	if n == 1 {
		return nil
	}
	d, e := r.Result(ctx, no, 0)
	if e != nil {
		return e
	}
	if d.Mode == "manual" && d.Status == "delivered" {
		return nil
	}
	return bad("该记录不需要人工确认发放")
}
func (r *Repo) AuditReveal(ctx context.Context, d *ent.LotteryDraw) error {
	_, e := data.Client(ctx, r.Data).SecurityAuditLog.Create().SetActorType(securityauditlog.ActorTypeAdmin).SetActorID(actor(ctx)).SetAction("lottery.reveal").SetMetadata(map[string]any{"draw_no": d.DrawNo, "user_id": d.UserID, "subsite_id": d.SubsiteID}).Save(ctx)
	return e
}
func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
