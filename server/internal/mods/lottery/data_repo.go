package lottery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"net/url"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryaccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryactivity"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotterychancelog"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryprize"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productcontrol"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
)

type Repo struct {
	Data   *data.Data
	Cipher *inventory.CardCipher
	now    func() time.Time
	random func() (int, error)
}

func NewRepo(d *data.Data, c *inventory.CardCipher) *Repo {
	return &Repo{Data: d, Cipher: c, now: time.Now, random: func() (int, error) {
		n, e := rand.Int(rand.Reader, big.NewInt(10000))
		if e != nil {
			return 0, e
		}
		return int(n.Int64()), nil
	}}
}
func bad(message string) error        { return errors.BadRequest("lottery.INVALID", message) }
func site(ctx context.Context) uint64 { return tenancy.FromContext(ctx).SubsiteID }
func actor(ctx context.Context) uint64 {
	if c := identity.ClaimsFromContext(ctx); c != nil {
		return c.Subject
	}
	return 0
}
func (r *Repo) member(ctx context.Context) (uint64, error) {
	id := actor(ctx)
	if id == 0 {
		return 0, errors.Unauthorized("lottery.LOGIN_REQUIRED", "请先登录")
	}
	ok, e := data.Client(ctx, r.Data).User.Query().Where(user.ID(id), user.StatusEQ(user.StatusActive)).Exist(ctx)
	if e != nil {
		return 0, e
	}
	if !ok {
		return 0, errors.Forbidden("lottery.USER_UNAVAILABLE", "账号不可参与抽奖")
	}
	return id, nil
}
func (r *Repo) activity(ctx context.Context, id uint64, lock bool) (*ent.LotteryActivity, error) {
	c := data.Client(ctx, r.Data)
	if lock {
		// 条件 UPDATE 持有事务行锁；MySQL 未变化行可能返回 0，存在性由下面查询判断。
		_, e := c.LotteryActivity.Update().Where(lotteryactivity.ID(id), lotteryactivity.SubsiteID(site(ctx))).AddRevision(0).Save(ctx)
		if e != nil {
			return nil, e
		}
	}
	a, e := c.LotteryActivity.Query().Where(lotteryactivity.ID(id), lotteryactivity.SubsiteID(site(ctx))).Only(ctx)
	if ent.IsNotFound(e) {
		return nil, bad("活动不存在")
	}
	return a, e
}
func (r *Repo) prizes(ctx context.Context, id uint64) ([]*ent.LotteryPrize, error) {
	return data.Client(ctx, r.Data).LotteryPrize.Query().Where(lotteryprize.ActivityID(id), lotteryprize.SubsiteID(site(ctx)), lotteryprize.Enabled(true)).Order(ent.Asc(lotteryprize.FieldSort), ent.Asc(lotteryprize.FieldID)).All(ctx)
}
func state(a *ent.LotteryActivity, now time.Time) string {
	if a.Published && a.Status != "archived" && !now.Before(a.EndAt) {
		return "ended"
	}
	if a.Status != "live" {
		return a.Status
	}
	if !now.Before(a.EndAt) {
		return "ended"
	}
	if now.Before(a.StartAt) {
		return "scheduled"
	}
	return "live"
}
func active(a *ent.LotteryActivity, now time.Time) error {
	if state(a, now) != "live" {
		return bad("活动未开始、已暂停或已结束")
	}
	return nil
}
func period(a *ent.LotteryActivity, now time.Time) string {
	if a.ChanceMode == "daily" {
		loc, e := time.LoadLocation(a.Timezone)
		if e == nil {
			return now.In(loc).Format("2006-01-02")
		}
	}
	return "once"
}

var keyRE = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

func requestKey(key string) error {
	if !keyRE.MatchString(key) {
		return bad("请求标识无效，请刷新页面后重试")
	}
	return nil
}
func newNo() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return "L" + hex.EncodeToString(b), nil
}
func validImage(s string) bool {
	if s == "" {
		return true
	}
	if strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") && !strings.ContainsAny(s, "\\\r\n") {
		return true
	}
	u, e := url.Parse(s)
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}

// eligibleCard 同时用于发布与每次抽奖；不绕过商品来源、分类隐藏、规格或控件要求。
func (r *Repo) eligibleCard(ctx context.Context, pid, sku uint64) (*ent.Product, error) {
	c := data.Client(ctx, r.Data)
	p, e := c.Product.Query().Where(product.ID(pid), product.SubsiteID(site(ctx)), product.Status(1)).Only(ctx)
	if e != nil {
		if ent.IsNotFound(e) {
			return nil, bad("自动发奖商品不存在或已下架")
		}
		return nil, e
	}
	if p.UpstreamSourceID != 0 || string(p.StockType) != "card" || string(p.DeliveryMode) != "status" {
		return nil, bad("自动发卡仅支持普通自营卡密和保留卡密模式")
	}
	seen := map[uint64]bool{}
	cid := p.CategoryID
	for cid != 0 {
		if seen[cid] {
			return nil, bad("商品分类层级异常")
		}
		seen[cid] = true
		cat, e := c.Category.Query().Where(category.ID(cid), category.SubsiteID(site(ctx))).Only(ctx)
		if e != nil {
			return nil, bad("商品分类不可用")
		}
		if cat.Hide {
			return nil, bad("商品分类已隐藏，请改用人工奖品")
		}
		cid = cat.ParentID
	}
	required, e := c.ProductControl.Query().Where(productcontrol.ProductID(pid), productcontrol.Required(true)).Exist(ctx)
	if e != nil {
		return nil, e
	}
	if required {
		return nil, bad("该商品有必填下单控件，请改用人工奖品")
	}
	if sku != 0 {
		ok, e := c.ProductSku.Query().Where(productsku.ID(sku), productsku.ProductID(pid), productsku.SubsiteID(site(ctx))).Exist(ctx)
		if e != nil {
			return nil, e
		}
		if !ok {
			return nil, bad("奖品规格不存在或不属于该商品")
		}
	} else {
		n, e := c.ProductSku.Query().Where(productsku.ProductID(pid)).Count(ctx)
		if e != nil {
			return nil, e
		}
		if n > 0 {
			return nil, bad("该商品有多个规格，请明确选择奖品规格")
		}
	}
	return p, nil
}
func (r *Repo) cardQuery(ctx context.Context, pid, sku uint64) *ent.CardQuery {
	q := data.Client(ctx, r.Data).Card.Query().Where(card.ProductID(pid), card.SubsiteID(site(ctx)), card.StatusEQ(card.StatusAvailable), card.NumberHashIsNil())
	if sku > 0 {
		return q.Where(card.SkuID(sku))
	}
	return q.Where(card.Or(card.SkuIDIsNil(), card.SkuID(0)))
}
func (r *Repo) ready(ctx context.Context, ps []*ent.LotteryPrize) error {
	total := 0
	for _, p := range ps {
		total += int(p.Probability)
		if p.Probability == 0 {
			continue
		}
		if p.Issued >= p.Quantity {
			return bad("奖品已发完，活动暂不可抽奖，请等待平台补充奖品")
		}
		if p.Mode == "card" {
			if _, e := r.eligibleCard(ctx, p.ProductID, p.SkuID); e != nil {
				return e
			}
			n, e := r.cardQuery(ctx, p.ProductID, p.SkuID).Count(ctx)
			if e != nil {
				return e
			}
			if n == 0 {
				return bad("奖品库存不足，活动暂不可抽奖，请等待平台补货")
			}
		}
	}
	if total <= 0 || total > 10000 {
		return bad("奖品概率配置无效")
	}
	return nil
}
func snapshot(a *ent.LotteryActivity, ps []*ent.LotteryPrize) map[string]any {
	prizes := []map[string]any{}
	for _, p := range ps {
		prizes = append(prizes, map[string]any{"id": p.ID, "name": p.Name, "mode": p.Mode, "probability": p.Probability, "quantity": p.Quantity, "product_id": p.ProductID, "sku_id": p.SkuID})
	}
	return map[string]any{"name": a.Name, "status": a.Status, "start_at": a.StartAt.Unix(), "end_at": a.EndAt.Unix(), "timezone": a.Timezone, "chance_mode": a.ChanceMode, "chance_count": a.ChanceCount, "prizes": prizes}
}
func (r *Repo) account(ctx context.Context, a *ent.LotteryActivity, uid uint64, now time.Time) (*ent.LotteryAccount, error) {
	c := data.Client(ctx, r.Data)
	per := period(a, now)
	// Activity row is held by caller: no competing insert for this activity/user/period.
	acc, e := c.LotteryAccount.Query().Where(lotteryaccount.SubsiteID(site(ctx)), lotteryaccount.ActivityID(a.ID), lotteryaccount.UserID(uid), lotteryaccount.Period(per)).Only(ctx)
	if ent.IsNotFound(e) {
		acc, e = c.LotteryAccount.Create().SetSubsiteID(site(ctx)).SetActivityID(a.ID).SetUserID(uid).SetPeriod(per).Save(ctx)
	}
	return acc, e
}
func (r *Repo) autoGrant(ctx context.Context, a *ent.LotteryActivity, acc *ent.LotteryAccount) error {
	if acc.AutoGranted || a.ChanceMode == "manual" {
		return nil
	}
	c := data.Client(ctx, r.Data)
	if _, e := c.LotteryChanceLog.Create().SetSubsiteID(site(ctx)).SetActivityID(a.ID).SetUserID(acc.UserID).SetPeriod(acc.Period).SetAmount(a.ChanceCount).SetKind("auto").SetRequestKey("auto:" + acc.Period).Save(ctx); e != nil {
		return e
	}
	if e := c.LotteryAccount.UpdateOneID(acc.ID).AddBalance(a.ChanceCount).SetAutoGranted(true).Exec(ctx); e != nil {
		return e
	}
	acc.Balance += a.ChanceCount
	acc.AutoGranted = true
	return nil
}
func (r *Repo) Claim(ctx context.Context, id, uid uint64) (int32, string, error) {
	var acc *ent.LotteryAccount
	e := data.Tx(ctx, r.Data, func(ctx context.Context) error {
		a, e := r.activity(ctx, id, true)
		if e != nil {
			return e
		}
		now := r.now()
		if e = active(a, now); e != nil {
			return e
		}
		acc, e = r.account(ctx, a, uid, now)
		if e != nil {
			return e
		}
		return r.autoGrant(ctx, a, acc)
	})
	if e != nil {
		return 0, "", e
	}
	return acc.Balance, acc.Period, nil
}
func (r *Repo) Grant(ctx context.Context, id, uid uint64, count int32, remark, key string) error {
	if count < 1 || count > 1000 || strings.TrimSpace(remark) == "" || len([]rune(remark)) > 500 {
		return bad("补发数量应为 1～1000 次，并填写原因")
	}
	if e := requestKey(key); e != nil {
		return e
	}
	return data.Tx(ctx, r.Data, func(ctx context.Context) error {
		a, e := r.activity(ctx, id, true)
		if e != nil {
			return e
		}
		c := data.Client(ctx, r.Data)
		old, e := c.LotteryChanceLog.Query().Where(lotterychancelog.SubsiteID(site(ctx)), lotterychancelog.ActivityID(id), lotterychancelog.UserID(uid), lotterychancelog.RequestKey("grant:"+key)).Only(ctx)
		if e == nil {
			if old.Amount != count || old.Remark != remark {
				return bad("请求标识已用于其他补发操作")
			}
			return nil
		}
		if !ent.IsNotFound(e) {
			return e
		}
		if !a.Published || a.Status == "archived" || !r.now().Before(a.EndAt) {
			return bad("仅可给尚未结束的已发布活动补发次数")
		}
		ok, e := c.User.Query().Where(user.ID(uid), user.StatusEQ(user.StatusActive)).Exist(ctx)
		if e != nil {
			return e
		}
		if !ok {
			return bad("用户不存在或已禁用")
		}
		acc, e := r.account(ctx, a, uid, r.now())
		if e != nil {
			return e
		}
		if acc.Balance > 100000-count {
			return bad("该账号剩余次数过多")
		}
		if _, e = c.LotteryChanceLog.Create().SetSubsiteID(site(ctx)).SetActivityID(id).SetUserID(uid).SetPeriod(acc.Period).SetAmount(count).SetKind("manual").SetRequestKey("grant:" + key).SetRemark(remark).SetAdminID(actor(ctx)).Save(ctx); e != nil {
			return e
		}
		return c.LotteryAccount.UpdateOneID(acc.ID).AddBalance(count).Exec(ctx)
	})
}
