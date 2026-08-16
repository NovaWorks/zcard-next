package memberlevel

// 会员等级仓储（M1b 基础版：CRUD + 按累计消费匹配折扣）。
// 升级自动化（支付成功事件异步评估、只升不降）M3 交付；此处实现同步「按累计消费取等级」。

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel/port"
)

// MemberLevelRepoImpl 等级仓储。
type MemberLevelRepoImpl struct {
	data *data.Data
}

// NewMemberLevelRepoImpl 构造。
func NewMemberLevelRepoImpl(d *data.Data) *MemberLevelRepoImpl {
	return &MemberLevelRepoImpl{data: d}
}

var _ port.RateResolver = (*MemberLevelRepoImpl)(nil)

// ListLevels 等级列表（按 sort 升序）。
func (r *MemberLevelRepoImpl) ListLevels(ctx context.Context) ([]*ent.MemberLevel, error) {
	return data.Client(ctx, r.data).MemberLevel.Query().
		Order(ent.Asc(memberlevel.FieldSort)).
		All(ctx)
}

// CreateLevel 创建等级。
func (r *MemberLevelRepoImpl) CreateLevel(ctx context.Context, name string, thresholdType string, thresholdRecharge, thresholdConsume int64, discount int32, sort int32, enabled bool) (*ent.MemberLevel, error) {
	return data.Client(ctx, r.data).MemberLevel.Create().
		SetName(name).
		SetThresholdType(memberlevel.ThresholdType(thresholdType)).
		SetThresholdRecharge(thresholdRecharge).
		SetThresholdConsume(thresholdConsume).
		SetDiscount(discount).
		SetSort(sort).
		SetEnabled(enabled).
		Save(ctx)
}

// UpdateLevel 更新等级。
func (r *MemberLevelRepoImpl) UpdateLevel(ctx context.Context, id uint64, name string, discount int32, sort int32, enabled bool) (*ent.MemberLevel, error) {
	return data.Client(ctx, r.data).MemberLevel.UpdateOneID(id).
		SetName(name).
		SetDiscount(discount).
		SetSort(sort).
		SetEnabled(enabled).
		Save(ctx)
}

// DeleteLevel 删除等级。
func (r *MemberLevelRepoImpl) DeleteLevel(ctx context.Context, id uint64) error {
	return data.Client(ctx, r.data).MemberLevel.DeleteOneID(id).Exec(ctx)
}

// EffectiveRate 按累计消费匹配最高 enabled 等级（threshold_type=consume 优先，recharge 走累计充值）。
// M1b 基础版：consume 口径 = 用户已支付订单总额；recharge 口径 M3 接钱包流水。
func (r *MemberLevelRepoImpl) EffectiveRate(ctx context.Context, userID uint64) (int32, uint64, error) {
	if userID == 0 {
		return 0, 0, nil
	}
	client := data.Client(ctx, r.data)
	levels, err := client.MemberLevel.Query().
		Where(memberlevel.Enabled(true)).
		Order(ent.Desc(memberlevel.FieldDiscount)).
		All(ctx)
	if err != nil || len(levels) == 0 {
		return 0, 0, err
	}

	// 累计消费（已支付及之后状态）
	consume := int64(0)
	if sum, err := client.Order.Query().
		Where(order.UserID(userID), order.StatusNotIn(
			order.StatusPendingPayment, order.StatusCanceled, order.StatusExpired,
		)).
		Aggregate(ent.Sum(order.FieldTotalAmount)).Int(ctx); err == nil {
		consume = int64(sum)
	}

	for _, lv := range levels {
		switch lv.ThresholdType {
		case memberlevel.ThresholdTypeConsume:
			if consume >= lv.ThresholdConsume {
				return lv.Discount, lv.ID, nil
			}
		case memberlevel.ThresholdTypeBothOr:
			if consume >= lv.ThresholdConsume {
				return lv.Discount, lv.ID, nil
			}
		default:
			// recharge / both_and 基础版不落地（M3 接钱包流水后生效）
		}
	}
	return 0, 0, nil
}
