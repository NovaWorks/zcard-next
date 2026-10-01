package data

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
)

// Pricing includes the immutable account identity and all rules that can change
// retail price. Use the same connection snapshot that made the network request.
func SMSRetailPricingRevision(conn *ent.SupplyConnection, m *ent.SupplyMapping) string {
	b, _ := json.Marshal([]any{SMSConnectionIdentity(conn), conn.Status, conn.ExchangeRate, conn.PriceMarkupPercent, conn.PriceMarkupAmount, conn.PriceRoundingMode, m.PricingOverride})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func SMSRetailCurrentRevision(ctx context.Context, c *ent.Client, p *ent.Product) (string, error) {
	conn, e := c.SupplyConnection.Get(ctx, p.UpstreamSourceID)
	if e != nil {
		return "", e
	}
	m, e := c.SupplyMapping.Query().Where(supplymapping.ConnectionID(conn.ID), supplymapping.LocalProductID(p.ID), supplymapping.UpstreamProduct(p.UpstreamProductCode), supplymapping.UpstreamSku("")).Only(ctx)
	if e != nil {
		return "", e
	}
	return SMSRetailPricingRevision(conn, m), nil
}
