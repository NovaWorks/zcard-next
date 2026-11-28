package catalog

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/NovaWorks/zcard-next/server/internal/platform/upstreamsource"
)

// BackfillUpstreamNavigation preserves existing navigation and frozen order snapshots.
func (r *ProductRepoImpl) BackfillUpstreamNavigation(ctx context.Context, connectionID uint64, code, pageID string) error {
	navigation := upstreamsource.Navigation(connectionID, code, pageID)
	if navigation == nil {
		return nil
	}
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		c := data.Client(ctx, r.data)
		p, err := c.Product.Query().Where(product.SubsiteID(tenancy.FromContext(ctx).SubsiteID), product.UpstreamSourceID(connectionID), product.UpstreamProductCode(code), product.StatusGTE(0)).Only(ctx)
		if ent.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		p, err = data.GuardProductWrite(ctx, r.data, p.ID)
		if err != nil {
			return err
		}
		if p.UpstreamSourceID != connectionID || p.UpstreamProductCode != code || upstreamsource.PageID(p.UpstreamNavigation, connectionID, code) != "" {
			return nil
		}
		if err := data.GuardUpstreamDelivery(ctx, c, p); err != nil {
			return err
		}
		return c.Product.UpdateOneID(p.ID).SetUpstreamNavigation(navigation).Exec(ctx)
	})
}
