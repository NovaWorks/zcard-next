package supply

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
)

func (s *SyncService) maintenanceProtected(ctx context.Context, connectionID uint64, code string) (bool, error) {
	c := s.repo.entClient(ctx)
	p, e := c.Product.Query().Where(product.UpstreamSourceID(connectionID), product.UpstreamProductCode(code)).First(ctx)
	if ent.IsNotFound(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if p.IsLocked || p.Status < 0 {
		return true, nil
	}
	return data.HasLocalDelivery(ctx, c, p)
}
