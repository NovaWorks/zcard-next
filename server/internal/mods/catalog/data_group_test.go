package catalog

import (
	"context"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

func TestResolveGroupDiscountCarriesSelectedPolicy(t *testing.T) {
	d, _ := newStatsEnv(t)
	r := NewProductRepoImpl(d, nil)
	ctx := context.Background()
	d.Client.MemberProductGroup.Create().SetName("other product").SetProductIds([]uint64{2}).SetDiscount(1000).SaveX(ctx)
	d.Client.MemberProductGroup.Create().SetName("invalid").SetProductIds([]uint64{1}).SetDiscount(0).SaveX(ctx)
	d.Client.MemberProductGroup.Create().SetName("other tenant").SetSubsiteID(9).SetProductIds([]uint64{1}).SetDiscount(2000).SaveX(ctx)
	d.Client.MemberProductGroup.Create().SetName("weaker").SetProductIds([]uint64{1}).SetDiscount(9500).SetStackMember(true).SetStackCoupon(false).SaveX(ctx)
	selected := d.Client.MemberProductGroup.Create().SetName("selected").SetProductIds([]uint64{1}).SetDiscount(9000).SetStackMember(false).SetStackCoupon(true).SaveX(ctx)
	d.Client.MemberProductGroup.Create().SetName("same rate later").SetProductIds([]uint64{1}).SetDiscount(9000).SetStackMember(true).SetStackCoupon(false).SaveX(ctx)
	g, err := r.ResolveGroupDiscount(ctx, 1)
	if err != nil || g.ID != selected.ID || g.Rate != 9000 || g.StackMember || !g.StackCoupon {
		t.Fatalf("selected rate and policy disagree: %+v err=%v", g, err)
	}
	g, err = r.ResolveGroupDiscount(ctx, 999)
	if err != nil || g.Rate != 0 || g.ID != 0 {
		t.Fatalf("unmatched group: %+v err=%v", g, err)
	}
	g, err = r.ResolveGroupDiscount(tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 9}), 1)
	if err != nil || g.Rate != 2000 {
		t.Fatalf("tenant group: %+v err=%v", g, err)
	}
}
