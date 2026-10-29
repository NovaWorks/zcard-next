package data

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/socialtask"
	"math/big"
)

// SocialConnectionIdentity excludes pricing and enabled state: accepted work must
// still be recoverable after sales are paused, but never under another account.
func SocialConnectionIdentity(c *ent.SupplyConnection) string {
	b, _ := json.Marshal([]any{c.ID, c.Driver, c.BaseURL, c.Credentials})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func SocialRetailPricingRevision(c *ent.SupplyConnection, m *ent.SupplyMapping) string {
	b, _ := json.Marshal([]any{SocialConnectionIdentity(c), c.Status, c.ExchangeRate, c.PriceMarkupPercent, c.PriceMarkupAmount, c.PriceRoundingMode, m.PricingOverride})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// SocialRetailRefundTarget converts a confirmed cumulative supply refund to the
// buyer's own paid amount. Integer arithmetic preserves the full-refund remainder.
func SocialRetailRefundTarget(paid, charged, refunded int64) (int64, error) {
	if paid <= 0 || charged <= 0 || refunded < 0 || refunded > charged {
		return 0, fmt.Errorf("invalid social refund amounts")
	}
	n := new(big.Int).Mul(big.NewInt(paid), big.NewInt(refunded))
	return n.Quo(n, big.NewInt(charged)).Int64(), nil
}
func HasSocialOrder(ctx context.Context, c *ent.Client, orderID uint64) (bool, error) {
	return c.OrderItem.Query().Where(orderitem.OrderID(orderID), orderitem.DeliveryKind("social_service")).Exist(ctx)
}

// SocialRetailRefundLimit is the cumulative buyer entitlement, not a new credit.
// An uncharged authoritative rejection is the only refund without a supply debit.
func SocialRetailRefundLimit(ctx context.Context, c *ent.Client, orderID uint64) (int64, bool, error) {
	has, e := HasSocialOrder(ctx, c, orderID)
	if e != nil || !has {
		return 0, has, e
	}
	rows, e := c.SocialTask.Query().Where(socialtask.Role("retail"), socialtask.OrderID(orderID)).All(ctx)
	if e != nil {
		return 0, true, e
	}
	if len(rows) != 1 {
		return 0, true, fmt.Errorf("服务采购凭证缺失")
	}
	row := rows[0]
	if row.UpstreamOrderID == "" || row.RefundReference == "" || row.RetailPaidAmount <= 0 {
		return 0, true, fmt.Errorf("服务供货退款凭证尚未确认")
	}
	if row.ChargedAmount == 0 && row.State == "rejected" && row.SettlementState == "refunded" && row.RefundReference == "rejected:"+row.UpstreamOrderID {
		return row.RetailPaidAmount, true, nil
	}
	if row.SettlementState != "refunded" && row.SettlementState != "partially_refunded" {
		return 0, true, fmt.Errorf("服务供货退款尚未确认")
	}
	target, e := SocialRetailRefundTarget(row.RetailPaidAmount, row.ChargedAmount, row.RefundedAmount)
	return target, true, e
}
func GuardSocialRefund(ctx context.Context, c *ent.Client, orderID uint64) error {
	_, _, e := SocialRetailRefundLimit(ctx, c, orderID)
	return e
}
