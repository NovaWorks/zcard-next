package coupon

// 优惠券仓储（M1b 基础版：批量生成 + 校验核销 + 作废）。

import (
	"context"
	"fmt"
	"strings"
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

// ListCoupons 券列表（状态/批次筛选 + 分页；total 与批次去重列表供前端筛选）。
func (r *CouponRepoImpl) ListCoupons(ctx context.Context, status, batchID string, page, size int) ([]*ent.Coupon, int, []string, error) {
	client := data.Client(ctx, r.data)
	q := client.Coupon.Query()
	if status != "" {
		q = q.Where(coupon.StatusEQ(coupon.Status(status)))
	}
	if batchID != "" {
		q = q.Where(coupon.BatchID(batchID))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 20
	}
	rows, err := q.Clone().Order(ent.Desc(coupon.FieldID)).Offset((page - 1) * size).Limit(size).All(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	// 批次去重列表（供筛选下拉；batch_id 建有索引）
	all, err := client.Coupon.Query().Select(coupon.FieldBatchID).All(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	seen := make(map[string]struct{}, len(all))
	batches := make([]string, 0, len(all))
	for _, c := range all {
		if c.BatchID != "" {
			if _, ok := seen[c.BatchID]; !ok {
				seen[c.BatchID] = struct{}{}
				batches = append(batches, c.BatchID)
			}
		}
	}
	return rows, total, batches, nil
}

// DeleteCoupons 按 id 批量删除（仅未使用；已使用/已作废跳过保审计痕迹）。
func (r *CouponRepoImpl) DeleteCoupons(ctx context.Context, ids []uint64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	return data.Client(ctx, r.data).Coupon.Delete().
		Where(coupon.IDIn(ids...), coupon.StatusEQ(coupon.StatusUnused)).
		Exec(ctx)
}

// DeleteBatchUnused 删除整批次全部未使用券。
func (r *CouponRepoImpl) DeleteBatchUnused(ctx context.Context, batchID string) (int, error) {
	if batchID == "" {
		return 0, nil
	}
	return data.Client(ctx, r.data).Coupon.Delete().
		Where(coupon.BatchID(batchID), coupon.StatusEQ(coupon.StatusUnused)).
		Exec(ctx)
}

// ExportCSV 导出券码 CSV（状态/批次筛选；含 BOM + 表头，Excel 直开）。
func (r *CouponRepoImpl) ExportCSV(ctx context.Context, status, batchID string) (string, error) {
	q := data.Client(ctx, r.data).Coupon.Query()
	if status != "" {
		q = q.Where(coupon.StatusEQ(coupon.Status(status)))
	}
	if batchID != "" {
		q = q.Where(coupon.BatchID(batchID))
	}
	rows, err := q.Order(ent.Asc(coupon.FieldID)).Limit(50000).All(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("\uFEFF批次,名称,类型,面值,券码,状态,过期时间\n")
	for _, c := range rows {
		typ := "满减"
		val := fmt.Sprintf("%d分", c.Value)
		if c.Type == coupon.TypePercent {
			typ = "折扣"
			val = fmt.Sprintf("%g折", float64(c.Value)/1000) // 万分比 → 折
		}
		statusZh := map[string]string{"unused": "未使用", "used": "已使用", "disabled": "已作废"}[string(c.Status)]
		if statusZh == "" {
			statusZh = string(c.Status)
		}
		expire := ""
		if !c.ExpireAt.IsZero() {
			expire = c.ExpireAt.Local().Format("2006-01-02 15:04")
		}
		// 名称/批次含逗号时加引号转义（RFC 4180）
		fmt.Fprintf(&b, "%s,%s,%s,%s,%s,%s,%s\n", csvEscape(c.BatchID), csvEscape(c.Name), typ, val, c.Code, statusZh, expire)
	}
	return b.String(), nil
}

// csvEscape CSV 字段含逗号/引号/换行时按 RFC 4180 加引号。
func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n\r") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
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
