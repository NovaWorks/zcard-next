package memberlevel

// 会员等级仓储（ ：阈值即时评估全矩阵 + 积分产生 + 等级进度）。
//
// 口径（1.x 铁律平移）：
// - 累计充值 = countAsRecharge：仅 type=recharge 真实充值入账（互转/调账/佣金不计——防小号互转刷级）
// - 累计消费 = 已支付及之后状态订单总额
// - 等级判定走阈值即时评估，不落 users 列（无双写不一致）；只升不降随累计值单调
// - 等级阶梯按 sort 升序：当前级 = 命中的最高 sort；下一级 = 其后首个未命中级

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
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
func (r *MemberLevelRepoImpl) CreateLevel(ctx context.Context, name string, thresholdType string, thresholdRecharge, thresholdConsume int64, discount int32, sort int32, enabled bool, pointsRule map[string]any) (*ent.MemberLevel, error) {
	create := data.Client(ctx, r.data).MemberLevel.Create().
		SetName(name).
		SetThresholdType(memberlevel.ThresholdType(thresholdType)).
		SetThresholdRecharge(thresholdRecharge).
		SetThresholdConsume(thresholdConsume).
		SetDiscount(discount).
		SetSort(sort).
		SetEnabled(enabled)
	if len(pointsRule) > 0 {
		create.SetPointsRule(pointsRule)
	}
	return create.Save(ctx)
}

// UpdateLevel 更新等级。
func (r *MemberLevelRepoImpl) UpdateLevel(ctx context.Context, id uint64, name string, discount int32, sort int32, enabled bool, pointsRule map[string]any) (*ent.MemberLevel, error) {
	q := data.Client(ctx, r.data).MemberLevel.UpdateOneID(id).
		SetName(name).
		SetDiscount(discount).
		SetSort(sort).
		SetEnabled(enabled)
	if pointsRule != nil {
		if len(pointsRule) == 0 {
			q = q.ClearPointsRule()
		} else {
			q = q.SetPointsRule(pointsRule)
		}
	}
	return q.Save(ctx)
}

// DeleteLevel 删除等级。
func (r *MemberLevelRepoImpl) DeleteLevel(ctx context.Context, id uint64) error {
	return data.Client(ctx, r.data).MemberLevel.DeleteOneID(id).Exec(ctx)
}

// effectiveLevel 阈值即时评估：命中的最高 sort 等级（全矩阵 recharge/consume/both_and/both_or）。
func (r *MemberLevelRepoImpl) effectiveLevel(ctx context.Context, userID uint64) (*ent.MemberLevel, error) {
	if userID == 0 {
		return nil, nil
	}
	client := data.Client(ctx, r.data)
	levels, err := client.MemberLevel.Query().
		Where(memberlevel.Enabled(true)).
		Order(ent.Asc(memberlevel.FieldSort)).
		All(ctx)
	if err != nil || len(levels) == 0 {
		return nil, err
	}
	recharged, consumed, err := r.cumulative(ctx, client, userID)
	if err != nil {
		return nil, err
	}
	var current *ent.MemberLevel
	for _, lv := range levels {
		if matchLevel(lv, recharged, consumed) {
			current = lv // sort 升序遍历，后者覆盖——保留最高命中
		}
	}
	return current, nil
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
	consumed := int64(0)
	if sum, err := client.Order.Query().
		Where(order.UserID(userID), order.StatusNotIn(
			order.StatusPendingPayment, order.StatusCanceled, order.StatusExpired,
		)).
		Aggregate(ent.Sum(order.FieldTotalAmount)).Int(ctx); err == nil {
		consumed = int64(sum)
	}
	return recharged, consumed, nil
}

// matchLevel 阈值矩阵判定（AND|OR）。
func matchLevel(lv *ent.MemberLevel, recharged, consumed int64) bool {
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
	p := &Progress{RechargeGap: -1, ConsumeGap: -1}
	client := data.Client(ctx, r.data)
	levels, err := client.MemberLevel.Query().
		Where(memberlevel.Enabled(true)).
		Order(ent.Asc(memberlevel.FieldSort)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	recharged, consumed, err := r.cumulative(ctx, client, userID)
	if err != nil {
		return nil, err
	}
	p.RechargedCents, p.ConsumedCents = recharged, consumed

	// 当前级 = 命中的最高 sort；下一级 = 其后首个未命中级
	lastMatched := -1
	for i, lv := range levels {
		if matchLevel(lv, recharged, consumed) {
			p.Current = lv
			lastMatched = i
		}
	}
	if lastMatched >= 0 && lastMatched+1 < len(levels) {
		p.Next = levels[lastMatched+1]
	} else if lastMatched < 0 && len(levels) > 0 {
		p.Next = levels[0] // 尚未入门：下一级 = 首级
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
