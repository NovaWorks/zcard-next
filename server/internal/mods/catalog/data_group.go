package catalog

// 会员商品组数据层（M1b）：admin CRUD + 订单管线商品组折扣解析。
// ent import 收口：data 前缀文件（架构测试规则 3b）。

import (
	"context"
	"fmt"
	"slices"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/memberproductgroup"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// GroupInput 会员商品组创建/更新输入。
type GroupInput struct {
	Name        string
	ProductIDs  []uint64
	Discount    int32 // 万分比
	StackMember bool
	StackCoupon bool
	BadgeStyle  string
}

// ── admin CRUD ───────────────────────────────────────────────

// ListMemberGroups 商品组列表。
func (r *ProductRepoImpl) ListMemberGroups(ctx context.Context) ([]*ent.MemberProductGroup, error) {
	tc := tenancy.FromContext(ctx)
	return data.Client(ctx, r.data).MemberProductGroup.Query().
		Where(memberproductgroup.SubsiteID(tc.SubsiteID)).
		Order(ent.Asc(memberproductgroup.FieldID)).
		All(ctx)
}

// CreateMemberGroup 创建商品组。
func (r *ProductRepoImpl) CreateMemberGroup(ctx context.Context, in GroupInput) (*ent.MemberProductGroup, error) {
	tc := tenancy.FromContext(ctx)
	return data.Client(ctx, r.data).MemberProductGroup.Create().
		SetSubsiteID(tc.SubsiteID).
		SetName(in.Name).
		SetProductIds(in.ProductIDs).
		SetDiscount(in.Discount).
		SetStackMember(in.StackMember).
		SetStackCoupon(in.StackCoupon).
		SetBadgeStyle(in.BadgeStyle).
		Save(ctx)
}

// UpdateMemberGroup 更新商品组（零值/空字段不动）。
func (r *ProductRepoImpl) UpdateMemberGroup(ctx context.Context, id uint64, in GroupInput) (*ent.MemberProductGroup, error) {
	q := data.Client(ctx, r.data).MemberProductGroup.UpdateOneID(id)
	if in.Name != "" {
		q.SetName(in.Name)
	}
	if in.ProductIDs != nil {
		q.SetProductIds(in.ProductIDs)
	}
	if in.Discount > 0 {
		q.SetDiscount(in.Discount)
	}
	if in.BadgeStyle != "" {
		q.SetBadgeStyle(in.BadgeStyle)
	}
	q.SetStackMember(in.StackMember).SetStackCoupon(in.StackCoupon)
	if err := q.Exec(ctx); err != nil {
		return nil, err
	}
	return data.Client(ctx, r.data).MemberProductGroup.Get(ctx, id)
}

// DeleteMemberGroup 删除商品组。
func (r *ProductRepoImpl) DeleteMemberGroup(ctx context.Context, id uint64) error {
	n, err := data.Client(ctx, r.data).MemberProductGroup.Delete().
		Where(memberproductgroup.ID(id)).Exec(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("catalog.GROUP_NOT_FOUND")
	}
	return nil
}

// ── 订单管线折扣解析 ─────────────────────────────────────────

// ResolveGroupRate 解析命中商品的会员商品组折扣（万分比；0=不命中）。
// enabled 语义：discount ∈ (0, 10000)；多组命中取最高折扣（最小 discount 值）。
func (r *ProductRepoImpl) ResolveGroupRate(ctx context.Context, productID uint64) (int32, error) {
	tc := tenancy.FromContext(ctx)
	rows, err := data.Client(ctx, r.data).MemberProductGroup.Query().
		Where(memberproductgroup.SubsiteID(tc.SubsiteID)).
		All(ctx)
	if err != nil {
		return 0, err
	}
	var best int32
	for _, g := range rows {
		if g.Discount <= 0 || g.Discount >= 10000 {
			continue // 无效折扣视为未启用
		}
		if !slices.Contains(g.ProductIds, productID) {
			continue
		}
		if best == 0 || g.Discount < best {
			best = g.Discount
		}
	}
	return best, nil
}
