package coupon

// 优惠券仓储（M1b 基础版：批量生成 + 校验核销 + 作废）。

import (
	"context"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// CouponRepoImpl 券仓储。
type CouponRepoImpl struct {
	data *data.Data
}

// NewCouponRepoImpl 构造。
func NewCouponRepoImpl(d *data.Data) *CouponRepoImpl {
	return &CouponRepoImpl{data: d}
}

var _ port.CouponResolver = (*CouponRepoImpl)(nil)

// ListCoupons 券列表。
func (r *CouponRepoImpl) ListCoupons(ctx context.Context, status string) ([]*ent.Coupon, error) {
	q := data.Client(ctx, r.data).Coupon.Query().Order(ent.Desc(coupon.FieldID)).Limit(100)
	if status != "" {
		q = q.Where(coupon.StatusEQ(coupon.Status(status)))
	}
	return q.All(ctx)
}

// CreateBatch 批量生成券码。
func (r *CouponRepoImpl) CreateBatch(ctx context.Context, name, typ string, value int64, count int32, expireAt *time.Time) (int32, error) {
	client := data.Client(ctx, r.data)
	batchID := fmt.Sprintf("B%d", time.Now().UnixNano())
	for i := int32(0); i < count; i++ {
		_, err := client.Coupon.Create().
			SetBatchID(batchID).
			SetName(name).
			SetType(coupon.Type(typ)).
			SetValue(value).
			SetCode(fmt.Sprintf("%s%04d", batchID, i)).
			SetNillableExpireAt(expireAt).
			SetStatus(coupon.StatusUnused).
			Save(ctx)
		if err != nil {
			return i, err
		}
	}
	return count, nil
}

// Disable 作废（按 batch_id）。
func (r *CouponRepoImpl) Disable(ctx context.Context, batchID string) (int, error) {
	return data.Client(ctx, r.data).Coupon.Update().
		Where(coupon.BatchID(batchID), coupon.StatusEQ(coupon.StatusUnused)).
		SetStatus(coupon.StatusDisabled).
		Save(ctx)
}

// Resolve 校验券并返回面额。
func (r *CouponRepoImpl) Resolve(ctx context.Context, code string, userID uint64, orderAmount money.Cents) (money.Cents, uint64, error) {
	if code == "" {
		return 0, 0, nil
	}
	client := data.Client(ctx, r.data)
	c, err := client.Coupon.Query().Where(coupon.Code(code)).Only(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("coupon.INVALID: %w", err)
	}
	if c.Status != coupon.StatusUnused {
		return 0, 0, fmt.Errorf("coupon.NOT_UNUSED")
	}
	if !c.ExpireAt.IsZero() && time.Now().UTC().After(c.ExpireAt) {
		return 0, 0, fmt.Errorf("coupon.EXPIRED")
	}
	if c.UserID != 0 && c.UserID != userID {
		return 0, 0, fmt.Errorf("coupon.USER_MISMATCH")
	}
	var value int64
	switch c.Type {
	case coupon.TypeFixed:
		value = c.Value
	case coupon.TypePercent:
		value = int64(orderAmount) * c.Value / 10000
	}
	if value > int64(orderAmount) {
		value = int64(orderAmount) // 券不找零
	}
	return money.Cents(value), c.ID, nil
}

// MarkUsed 核销。
func (r *CouponRepoImpl) MarkUsed(ctx context.Context, couponID, orderID uint64) error {
	now := time.Now().UTC()
	return data.Client(ctx, r.data).Coupon.UpdateOneID(couponID).
		SetStatus(coupon.StatusUsed).
		SetUsedAt(now).
		SetUsedOrderID(orderID).
		Exec(ctx)
}
