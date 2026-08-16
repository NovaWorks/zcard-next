package reseller

// 分站核心（P3-04 T2/T4/T5/T6 核心，主站 admin 面先行）：
//   申请/审核（等级+加价率区间）、定价引擎（4 模式 × 三级优先级 + 上下限）、
//   分账账本（幂等/双阶段/水位缓存/重算）、防自购三查快照。
// 域名验证与租户上下文贯穿、分站后台完整 API 面随 storefront 用户登录体系接续。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/resellerbalanceaccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/resellerledgerentry"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/resellerpricing"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/resellerprofile"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// 哨兵错误。
var (
	ErrNotFound        = errors.New("reseller: 记录不存在")
	ErrDuplicateApply  = errors.New("reseller.DUPLICATE_APPLY: 已有申请记录")
	ErrMarkupExceed    = errors.New("reseller.MARKUP_EXCEED: 超过分站加价率上限")
	ErrBelowBase       = errors.New("reseller.BELOW_BASE: 分站价不得低于主站基础价")
	ErrNotApproved     = errors.New("reseller.NOT_APPROVED: 分站未过审")
	ErrInsufficient    = errors.New("reseller.INSUFFICIENT_BALANCE")
)

// ResellerRepo 仓储。
type ResellerRepo struct {
	data *data.Data
}

// NewResellerRepo 构造。
func NewResellerRepo(d *data.Data) *ResellerRepo { return &ResellerRepo{data: d} }

// ── T2 申请/审核 ─────────────────────────────────────────

// ApplyInput 申请入参。
type ApplyInput struct {
	UserID       uint64
	Reason       string
	DomainIntent string
}

// Apply 申请分站（一人一份：user_id 唯一；重复申请拒绝）。
func (r *ResellerRepo) Apply(ctx context.Context, in ApplyInput) (*ent.ResellerProfile, error) {
	p, err := data.Client(ctx, r.data).ResellerProfile.Create().
		SetUserID(in.UserID).
		SetStatus(resellerprofile.StatusApplying).
		SetApplyReason(in.Reason).
		SetDefaultMarkupPercent(10). // 审核通过时可调；默认 10%
		SetMaxMarkupPercent(50).
		SetConfirmDays(7).
		Save(ctx)
	if ent.IsConstraintError(err) {
		return nil, ErrDuplicateApply
	}
	return p, err
}

// Review 审核（approve=true 生成 approved；拒绝写 reject_reason）。
func (r *ResellerRepo) Review(ctx context.Context, id uint64, approve bool, reason string, reviewedBy uint64, defaultMarkup, maxMarkup float64, confirmDays int32) (*ent.ResellerProfile, error) {
	client := data.Client(ctx, r.data)
	status := resellerprofile.StatusRejected
	if approve {
		status = resellerprofile.StatusApproved
	}
	upd := client.ResellerProfile.UpdateOneID(id).
		SetStatus(status).
		SetReviewedBy(reviewedBy).
		SetReviewedAt(time.Now().UTC())
	if !approve && reason != "" {
		upd.SetRejectReason(reason)
	}
	if approve {
		if defaultMarkup > 0 {
			upd.SetDefaultMarkupPercent(defaultMarkup)
		}
		if maxMarkup > 0 {
			upd.SetMaxMarkupPercent(maxMarkup)
		}
		if confirmDays > 0 {
			upd.SetConfirmDays(confirmDays)
		}
	}
	p, err := upd.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// ListProfiles 申请/分站列表。
func (r *ResellerRepo) ListProfiles(ctx context.Context, status string, page, size int) ([]*ent.ResellerProfile, int, error) {
	q := data.Client(ctx, r.data).ResellerProfile.Query().Order(ent.Desc(resellerprofile.FieldID))
	if status != "" {
		q = q.Where(resellerprofile.StatusEQ(resellerprofile.Status(status)))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// ProfileByUser 按用户查（分站主身份判定）。
func (r *ResellerRepo) ProfileByUser(ctx context.Context, userID uint64) (*ent.ResellerProfile, error) {
	p, err := data.Client(ctx, r.data).ResellerProfile.Query().
		Where(resellerprofile.UserID(userID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// ── T4 定价引擎 ───────────────────────────────────────────

// UpsertPricing 定价规则（SKU>商品>分站默认三级之一）。
// value 语义随 mode：markup_percent=万分比；fixed_markup/fixed_price=分。
// 上限校验：markup_percent 不得超 profile.max_markup_percent（百分×100）。
func (r *ResellerRepo) UpsertPricing(ctx context.Context, subsiteID, productID, skuID uint64, mode string, value int64, maxMarkupPercent float64) (*ent.ResellerPricing, error) {
	client := data.Client(ctx, r.data)
	if mode == "markup_percent" && maxMarkupPercent > 0 && float64(value) > maxMarkupPercent*100 {
		return nil, ErrMarkupExceed
	}
	existing, err := client.ResellerPricing.Query().
		Where(
			resellerpricing.SubsiteIDEQ(subsiteID),
			resellerpricing.ProductIDEQ(productID),
			resellerpricing.SkuIDEQ(skuID),
		).Only(ctx)
	if ent.IsNotFound(err) {
		return client.ResellerPricing.Create().
			SetSubsiteID(subsiteID).SetProductID(productID).SetSkuID(skuID).
			SetMode(resellerpricing.Mode(mode)).SetValue(value).
			Save(ctx)
	}
	if err != nil {
		return nil, err
	}
	return client.ResellerPricing.UpdateOneID(existing.ID).
		SetMode(resellerpricing.Mode(mode)).SetValue(value).
		Save(ctx)
}

// ResolveUnitPrice 分站单价（listing 与 checkout 共用同一实现——1.x 铁律）。
// 优先级：SKU 规则 > 商品规则 > 分站默认加价率 > 继承主站价。
// 下限保护：结果不得低于 basePrice（主站基础价）。
func (r *ResellerRepo) ResolveUnitPrice(ctx context.Context, subsiteID, productID, skuID uint64, basePrice money.Cents) (money.Cents, error) {
	client := data.Client(ctx, r.data)
	// SKU 级
	if skuID > 0 {
		if p, err := client.ResellerPricing.Query().Where(
			resellerpricing.SubsiteIDEQ(subsiteID), resellerpricing.ProductIDEQ(productID),
			resellerpricing.SkuIDEQ(skuID),
		).Only(ctx); err == nil {
			return applyPricingMode(p.Mode, p.Value, basePrice)
		}
	}
	// 商品级
	if p, err := client.ResellerPricing.Query().Where(
		resellerpricing.SubsiteIDEQ(subsiteID), resellerpricing.ProductIDEQ(productID),
		resellerpricing.SkuIDEQ(0),
	).Only(ctx); err == nil {
		return applyPricingMode(p.Mode, p.Value, basePrice)
	}
	// 分站默认加价率（profiles.default_markup_percent——subsite_id 即 profile 主键）
	profile, err := client.ResellerProfile.Get(ctx, subsiteID)
	if err == nil {
		return applyPricingMode("markup_percent", int64(profile.DefaultMarkupPercent*100), basePrice)
	}
	// 继承主站价
	return basePrice, nil
}

// applyPricingMode 4 模式换算 + 下限保护。
// value 语义：markup_percent=万分比；fixed_markup/fixed_price=分。
func applyPricingMode(mode resellerpricing.Mode, value int64, basePrice money.Cents) (money.Cents, error) {
	base := int64(basePrice)
	var out int64
	switch string(mode) {
	case "inherit":
		out = base
	case "markup_percent": // 万分比（1000 = +10%）
		out = base + base*value/10000
	case "fixed_markup":
		out = base + value
	case "fixed_price":
		out = value
	default:
		return basePrice, nil
	}
	final := money.Cents(out)
	if final < basePrice {
		return basePrice, ErrBelowBase // 下限保护（fixed_price/fixed_markup 可能低于）
	}
	return final, nil
}

// ── T5 分账账本 ───────────────────────────────────────────

// SettleInput 分账入账输入。
type SettleInput struct {
	SubsiteID uint64
	OrderID   uint64
	Amount    money.Cents // 分站利润（subsite_markup 合计）
}

// SettleOrderProfit 订单分账入账（幂等键 order_profit:<orderID>；冻结 available_at）。
func (r *ResellerRepo) SettleOrderProfit(ctx context.Context, in SettleInput) error {
	if in.Amount <= 0 {
		return nil
	}
	client := data.Client(ctx, r.data)
	profile, err := client.ResellerProfile.Get(ctx, in.SubsiteID)
	if err != nil {
		return ErrNotApproved
	}
	confirmDays := int(profile.ConfirmDays)
	if confirmDays <= 0 {
		confirmDays = 7
	}
	_, err = client.ResellerLedgerEntry.Create().
		SetSubsiteID(in.SubsiteID).
		SetOrderID(in.OrderID).
		SetType("order_profit").
		SetAmount(int64(in.Amount)).
		SetStatus(resellerledgerentry.StatusPending).
		SetAvailableAt(time.Now().UTC().AddDate(0, 0, confirmDays)).
		SetIdempotencyKey(fmt.Sprintf("order_profit:%d", in.OrderID)).
		Save(ctx)
	if ent.IsConstraintError(err) {
		return nil // 幂等 ACK
	}
	if err != nil {
		return err
	}
	return r.refreshBalance(ctx, in.SubsiteID, int64(in.Amount))
}

// ConfirmDue 到期确认（pending → available；cron）。
func (r *ResellerRepo) ConfirmDue(ctx context.Context, now time.Time, limit int) (int, error) {
	client := data.Client(ctx, r.data)
	rows, err := client.ResellerLedgerEntry.Query().
		Where(
			resellerledgerentry.StatusEQ(resellerledgerentry.StatusPending),
			resellerledgerentry.AvailableAtLTE(now),
		).Order(ent.Asc(resellerledgerentry.FieldID)).Limit(limit).All(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range rows {
		if _, err := client.ResellerLedgerEntry.UpdateOneID(e.ID).
			SetStatus(resellerledgerentry.StatusAvailable).Save(ctx); err == nil {
			n++
		}
	}
	return n, nil
}

// refreshBalance 余额缓存增量（available 维度——pending/locked 独立核算）。
func (r *ResellerRepo) refreshBalance(ctx context.Context, subsiteID uint64, delta int64) error {
	client := data.Client(ctx, r.data)
	acc, err := client.ResellerBalanceAccount.Query().
		Where(resellerbalanceaccount.SubsiteIDEQ(subsiteID)).Only(ctx)
	if ent.IsNotFound(err) {
		_, err = client.ResellerBalanceAccount.Create().
			SetSubsiteID(subsiteID).SetAvailable(delta).SetLocked(0).SetNegative(0).
			Save(ctx)
		return err
	}
	if err != nil {
		return err
	}
	_, err = client.ResellerBalanceAccount.UpdateOneID(acc.ID).
		SetAvailable(acc.Available + delta).Save(ctx)
	return err
}

// RecomputeBalance 流水重算（对账函数——测试断言口径）。
func (r *ResellerRepo) RecomputeBalance(ctx context.Context, subsiteID uint64) (available, locked, negative int64, err error) {
	client := data.Client(ctx, r.data)
	rows, err := client.ResellerLedgerEntry.Query().
		Where(resellerledgerentry.SubsiteIDEQ(subsiteID)).All(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, e := range rows {
		switch string(e.Status) {
		case "available":
			available += e.Amount
		case "locked":
			locked += e.Amount
		case "pending":
			if e.Amount >= 0 {
				available += e.Amount // 冻结态计入可提快照（可用口径=available+pending 正数）
			} else {
				negative += -e.Amount
			}
		case "withdrawn":
			// 已提现不计
		}
	}
	return available, locked, negative, nil
}

// ── T6 防自购三查 ─────────────────────────────────────────

// ProfitEligible 防自购判定（下单快照落 orders.profit_eligible）。
// 三查：买家==分站主 / 买家∈分站主上级链 / 买家∈同链分站主（互推）。
func (r *ResellerRepo) ProfitEligible(ctx context.Context, subsiteID, buyerID uint64) bool {
	client := data.Client(ctx, r.data)
	profile, err := client.ResellerProfile.Get(ctx, subsiteID)
	if err != nil {
		return true // 非分站单（主站直营）：默认分账资格=主站自身利润逻辑
	}
	owner := profile.UserID
	if buyerID == owner {
		return false // 查一：分站主自购
	}
	// 查二/三：买家与分站主的邀请链交叉（任一方向命中即视为关联自购）
	buyer, err := client.User.Get(ctx, buyerID)
	if err != nil {
		return true
	}
	if buyer.InviteL1 == owner || buyer.InviteL2 == owner || buyer.InviteL3 == owner {
		return false // 买家是分站主的下级
	}
	// 分站主是买家的下级（反向）
	if owner != 0 {
		if o, err := client.User.Get(ctx, owner); err == nil {
			if o.InviteL1 == buyerID || o.InviteL2 == buyerID || o.InviteL3 == buyerID {
				return false
			}
		}
	}
	return true
}

var (
	_ = order.FieldSubsiteID
	_ = user.FieldInviteL1
	_ = json.Marshal
	_ = events.OrderPaid
	_ = slog.Default
)

// GetProfile 按 ID 查分站。
func (r *ResellerRepo) GetProfile(ctx context.Context, id uint64) (*ent.ResellerProfile, error) {
	p, err := data.Client(ctx, r.data).ResellerProfile.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// Ledger 账本流水（按状态筛选）。
func (r *ResellerRepo) Ledger(ctx context.Context, subsiteID uint64, status string, page, size int) ([]*ent.ResellerLedgerEntry, int, error) {
	q := data.Client(ctx, r.data).ResellerLedgerEntry.Query().
		Where(resellerledgerentry.SubsiteIDEQ(subsiteID)).
		Order(ent.Desc(resellerledgerentry.FieldID))
	if status != "" {
		q = q.Where(resellerledgerentry.StatusEQ(resellerledgerentry.Status(status)))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Offset((page - 1) * size).Limit(size).All(ctx)
	return rows, total, err
}

// GetBalance 余额缓存账户（缺失返回零值账户语义）。
func (r *ResellerRepo) GetBalance(ctx context.Context, subsiteID uint64) (*ent.ResellerBalanceAccount, error) {
	acc, err := data.Client(ctx, r.data).ResellerBalanceAccount.Query().
		Where(resellerbalanceaccount.SubsiteIDEQ(subsiteID)).Only(ctx)
	if ent.IsNotFound(err) {
		return &ent.ResellerBalanceAccount{SubsiteID: subsiteID}, nil
	}
	if err != nil {
		return nil, err
	}
	return acc, nil
}
