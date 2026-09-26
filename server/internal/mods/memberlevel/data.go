package memberlevel

// 会员等级仓储（ ：阈值即时评估全矩阵 + 积分产生 + 等级进度）。
//
// 口径（1.x 铁律平移）：
// - 累计充值 = countAsRecharge：仅 type=recharge 真实充值入账（互转/调账/佣金不计——防小号互转刷级）
// - 累计消费 = 已支付及之后状态订单总额，排除已全额退款订单
// - 等级判定走阈值即时评估，不落 users 列（无双写不一致）
// - 等级阶梯按 sort 升序：当前级 = 命中的最高 sort；下一级 = 其后首个未命中级

import (
	"context"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel/port"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
)

// MemberLevelRepoImpl 等级仓储。
type MemberLevelRepoImpl struct {
	data     *data.Data
	recharge walletport.RechargeReader // countAsRecharge 口径（nil = 充值口径恒 0）
}

// NewMemberLevelRepoImpl 构造。
func NewMemberLevelRepoImpl(d *data.Data, recharge walletport.RechargeReader) *MemberLevelRepoImpl {
	return &MemberLevelRepoImpl{data: d, recharge: recharge}
}

var _ port.RateResolver = (*MemberLevelRepoImpl)(nil)

// ListLevels 等级列表（按 sort 升序）。
func (r *MemberLevelRepoImpl) ListLevels(ctx context.Context) ([]*ent.MemberLevel, error) {
	return data.Client(ctx, r.data).MemberLevel.Query().
		Order(ent.Asc(memberlevel.FieldSort)).
		All(ctx)
}

// CreateLevel 创建等级（points_rule JSON 透传：{"spend_cents":X,"points":Y}）。
func (r *MemberLevelRepoImpl) CreateLevel(ctx context.Context, name string, thresholdType string, thresholdRecharge, thresholdConsume int64, discount int32, sort int32, enabled bool, pointsRule map[string]any, settings ...LevelSettings) (*ent.MemberLevel, error) {
	v := LevelSettings{}
	if len(settings) > 0 {
		v = settings[0]
	}
	if err := v.validate(); err != nil {
		return nil, err
	}
	if discount < 0 || discount > 10000 {
		return nil, fmt.Errorf("折扣应为0至10000")
	}
	create := data.Client(ctx, r.data).MemberLevel.Create().
		SetName(name).
		SetThresholdType(memberlevel.ThresholdType(thresholdType)).
		SetThresholdRecharge(thresholdRecharge).
		SetThresholdConsume(thresholdConsume).
		SetDiscount(discount).
		SetSort(sort).
		SetEnabled(enabled)
	if v.AcquireMode != "" {
		create.SetAcquireMode(v.AcquireMode)
	}
	if v.DisplayMode != "" {
		create.SetDisplayMode(v.DisplayMode)
	}
	if len(pointsRule) > 0 {
		create.SetPointsRule(pointsRule)
	}
	return create.Save(ctx)
}

// UpdateLevel 更新等级。
func (r *MemberLevelRepoImpl) UpdateLevel(ctx context.Context, id uint64, name string, discount int32, sort int32, enabled bool, pointsRule map[string]any, settings ...LevelSettings) (*ent.MemberLevel, error) {
	v := LevelSettings{}
	if len(settings) > 0 {
		v = settings[0]
	}
	if err := v.validate(); err != nil {
		return nil, err
	}
	if discount < 0 || discount > 10000 {
		return nil, fmt.Errorf("折扣应为0至10000")
	}
	var result *ent.MemberLevel
	err := data.Tx(ctx, r.data, func(ctx context.Context) error {
		if err := r.lockLevel(ctx, id); err != nil {
			return err
		}

		if !enabled {
			if err := r.ensureUnassigned(ctx, id); err != nil {
				return err
			}
		}
		q := data.Client(ctx, r.data).MemberLevel.UpdateOneID(id).
			SetName(name).
			SetDiscount(discount).
			SetSort(sort).
			SetEnabled(enabled)
		if v.AcquireMode != "" {
			q.SetAcquireMode(v.AcquireMode)
		}
		if v.DisplayMode != "" {
			q.SetDisplayMode(v.DisplayMode)
		}
		if pointsRule != nil {
			if len(pointsRule) == 0 {
				q = q.ClearPointsRule()
			} else {
				q = q.SetPointsRule(pointsRule)
			}
		}
		var err error
		result, err = q.Save(ctx)
		return err
	})
	return result, err
}

// DeleteLevel 删除等级。
func (r *MemberLevelRepoImpl) DeleteLevel(ctx context.Context, id uint64) error {
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		if err := r.lockLevel(ctx, id); err != nil {
			return err
		}
		if err := r.ensureUnassigned(ctx, id); err != nil {
			return err
		}
		return data.Client(ctx, r.data).MemberLevel.DeleteOneID(id).Exec(ctx)
	})
}

// effectiveLevel 阈值即时评估：命中的最高 sort 等级（全矩阵 recharge/consume/both_and/both_or）。
func (r *MemberLevelRepoImpl) effectiveLevel(ctx context.Context, userID uint64) (*ent.MemberLevel, error) {
	if userID == 0 {
		return nil, nil
	}
	p, err := r.resolveProgress(ctx, userID, false)
	if err != nil {
		return nil, err
	}
	return p.Current, nil
}

// payableRate normalizes the existing zero sentinel (no discount).
func payableRate(lv *ent.MemberLevel) int32 {
	if lv == nil || lv.Discount <= 0 || lv.Discount >= 10000 {
		return 10000
	}
	return lv.Discount
}

// cumulative 双口径累计（充值 countAsRecharge + 消费 paid+）。
func (r *MemberLevelRepoImpl) cumulative(ctx context.Context, client *ent.Client, userID uint64) (int64, int64, error) {
	var recharged int64
	if r.recharge != nil {
		v, err := r.recharge.CumulativeRecharge(ctx, userID)
		if err != nil {
			return 0, 0, err
		}
		recharged = v
	}
	totals, err := data.UserSpending(ctx, client, []uint64{userID})
	if err != nil {
		return 0, 0, err
	}
	return recharged, totals[userID], nil
}

// matchLevel 阈值矩阵判定（AND|OR）。
func matchLevel(lv *ent.MemberLevel, recharged, consumed int64) bool {
	if lv.AcquireMode == "manual" {
		return false
	}
	rc := lv.ThresholdRecharge <= 0 || recharged >= lv.ThresholdRecharge
	cc := lv.ThresholdConsume <= 0 || consumed >= lv.ThresholdConsume
	switch lv.ThresholdType {
	case memberlevel.ThresholdTypeBothAnd:
		return rc && cc
	case memberlevel.ThresholdTypeBothOr:
		return rc || cc
	case memberlevel.ThresholdTypeConsume:
		return cc
	default: // recharge
		return rc
	}
}

// EffectiveRate 管线步骤 2：当前等级折扣（万分比；未命中 0）。
func (r *MemberLevelRepoImpl) EffectiveRate(ctx context.Context, userID uint64) (int32, uint64, error) {
	lv, err := r.effectiveLevel(ctx, userID)
	if err != nil || lv == nil {
		return 0, 0, err
	}
	return lv.Discount, lv.ID, nil
}

// EffectiveLevelOf 当前等级实体（事件侧取 points_rule 消费）。
func (r *MemberLevelRepoImpl) EffectiveLevelOf(ctx context.Context, userID uint64) (*ent.MemberLevel, error) {
	return r.effectiveLevel(ctx, userID)
}

// Progress 等级进度视图（storefront GetMyLevel）。
type Progress struct {
	Source         string
	HasReferral    bool
	RechargedCents int64
	ConsumedCents  int64
	Current        *ent.MemberLevel // nil = 未命中任何等级
	Next           *ent.MemberLevel // nil = 已满级
	RechargeGap    int64            // 距下一级充值差额（-1 = 下一级无充值条件）
	ConsumeGap     int64            // 距下一级消费差额（-1 = 下一级无消费条件）
	Percent        int32            // 0-100
}

// ResolveProgress 进度解析（当前级 + 下一级 + 双口径差额）。
func (r *MemberLevelRepoImpl) ResolveProgress(ctx context.Context, userID uint64) (*Progress, error) {
	return r.resolveProgress(ctx, userID, true)
}

func (r *MemberLevelRepoImpl) resolveProgress(ctx context.Context, userID uint64, includeTotals bool) (*Progress, error) {
	p := &Progress{RechargeGap: -1, ConsumeGap: -1, Source: "none"}
	client := data.Client(ctx, r.data)
	u, err := client.User.Get(ctx, userID)
	if err != nil && !ent.IsNotFound(err) {
		return nil, err
	}
	p.HasReferral = u != nil && u.ReferralLevelID > 0
	if !includeTotals && u != nil && u.ManualLevelID > 0 {
		lv, err := r.assigned(ctx, userID)
		if err != nil {
			return nil, err
		}
		p.Current, p.Source = lv, "manual"
		return p, nil
	}
	levels, err := client.MemberLevel.Query().
		Where(memberlevel.Enabled(true), memberlevel.AcquireMode("auto")).
		Order(ent.Asc(memberlevel.FieldSort)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	var recharged, consumed int64
	if includeTotals || len(levels) > 0 {
		recharged, consumed, err = r.cumulative(ctx, client, userID)
		if err != nil {
			return nil, err
		}
	}
	p.RechargedCents, p.ConsumedCents = recharged, consumed
	if lv, e := r.assigned(ctx, userID); e != nil {
		return nil, e
	} else if lv != nil {
		p.Current = lv
		p.Source = "manual"
		return p, nil
	}

	// 当前级 = 命中的最高 sort；下一级 = 其后首个未命中级
	lastMatched := -1
	for i, lv := range levels {
		if matchLevel(lv, recharged, consumed) {
			p.Current = lv
			lastMatched = i
		}
	}
	if p.Current != nil {
		p.Source = "auto"
	}
	if p.HasReferral {
		granted, err := client.MemberLevel.Get(ctx, u.ReferralLevelID)
		if err != nil {
			return nil, err
		}
		if !granted.Enabled {
			return nil, fmt.Errorf("推荐赠送等级已停用，请联系管理员")
		}
		if p.Current == nil || payableRate(granted) < payableRate(p.Current) {
			p.Current, p.Source = granted, "referral"
		}
	}
	// 保留普通用户原有升级阶梯；获赠用户只展示确实能改善会员折扣的下一档。
	for i := lastMatched + 1; i < len(levels); i++ {
		if !p.HasReferral || payableRate(levels[i]) < payableRate(p.Current) {
			p.Next = levels[i]
			break
		}
	}

	if p.Next != nil {
		p.RechargeGap, p.ConsumeGap, p.Percent = nextGap(p.Next, recharged, consumed)
	}
	return p, nil
}

// nextGap 下一级差额与进度百分比（无该条件 -1；both_and 取短板）。
func nextGap(next *ent.MemberLevel, recharged, consumed int64) (int64, int64, int32) {
	rg, cg := int64(-1), int64(-1)
	if next.ThresholdRecharge > 0 {
		rg = maxI64(next.ThresholdRecharge-recharged, 0)
	}
	if next.ThresholdConsume > 0 {
		cg = maxI64(next.ThresholdConsume-consumed, 0)
	}
	var parts []int64
	if next.ThresholdRecharge > 0 {
		parts = append(parts, pct(recharged, next.ThresholdRecharge))
	}
	if next.ThresholdConsume > 0 {
		parts = append(parts, pct(consumed, next.ThresholdConsume))
	}
	percent := int32(100)
	for _, v := range parts {
		if v < int64(percent) {
			percent = int32(v)
		}
	}
	return rg, cg, percent
}

func pct(have, threshold int64) int64 {
	if threshold <= 0 {
		return 100
	}
	v := have * 100 / threshold
	if v > 100 {
		return 100
	}
	return v
}

func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// PointsRuleOf 积分产生规则解析（{"spend_cents":X,"points":Y}；未配置返回 0）。
func PointsRuleOf(lv *ent.MemberLevel) (spendCents, points int64) {
	if lv == nil || len(lv.PointsRule) == 0 {
		return 0, 0
	}
	// JSON 读回数值为 float64；ent 内存对象为 int64——两态兼容
	toI64 := func(v any) int64 {
		switch n := v.(type) {
		case int64:
			return n
		case float64:
			return int64(n)
		case int:
			return int64(n)
		}
		return 0
	}
	return toI64(lv.PointsRule["spend_cents"]), toI64(lv.PointsRule["points"])
}

func (r *MemberLevelRepoImpl) EffectiveState(ctx context.Context, userID uint64) (int32, uint64, string, error) {
	if userID == 0 {
		return 0, 0, "none", nil
	}
	p, err := r.resolveProgress(ctx, userID, false)
	if err != nil {
		return 0, 0, "none", err
	}
	if p.Current == nil {
		return 0, 0, p.Source, nil
	}
	return p.Current.Discount, p.Current.ID, p.Source, nil
}
