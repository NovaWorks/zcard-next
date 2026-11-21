package order

import (
	"context"
	"fmt"

	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// DiscountedPrice reads current membership on every request, so an admin level
// change takes effect without replacing the customer's login token.
func (uc *OrderUsecase) DiscountedPrice(ctx context.Context, productID uint64, base money.Cents) (money.Cents, error) {
	var memberRate int32
	if claims := identity.ClaimsFromContext(ctx); claims != nil && uc.MemberRate != nil {
		rate, _, err := uc.MemberRate.EffectiveRate(ctx, claims.Subject)
		if err != nil {
			return 0, fmt.Errorf("order.MEMBER_LEVEL_INVALID: %w", err)
		}
		memberRate = rate
	}
	var group catalogport.GroupDiscount
	if uc.Catalog != nil {
		var err error
		group, err = uc.Catalog.ResolveGroupDiscount(ctx, productID)
		if err != nil {
			return 0, fmt.Errorf("order.GROUP_LOOKUP_FAILED: %w", err)
		}
	}
	memberRate, groupRate := itemDiscountRates(memberRate, group)
	return PriceCalculator(PriceInput{BasePrice: base, Quantity: 1, MemberRate: memberRate, GroupRate: groupRate}).Total, nil
}

func itemDiscountRates(memberRate int32, group catalogport.GroupDiscount) (int32, int32) {
	groupRate := group.Rate
	if !group.StackMember && groupRate > 0 && groupRate < 10000 && memberRate > 0 && memberRate < 10000 {
		// Equal discounts prefer membership, matching checkout coupon eligibility.
		if memberRate <= groupRate {
			groupRate = 0
		} else {
			memberRate = 0
		}
	}
	return memberRate, groupRate
}
