package supplier

import (
	"context"
	"errors"

	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
)

// productForLegacySupply keeps detail/stock/valuation consistent with the
// protocol's single product-level pool, even when a catalog reader is stale.
func (s *SupplyAPIService) productForLegacySupply(ctx context.Context, id uint64) (*catalogport.SupplierProduct, error) {
	p, err := s.reader.GetForSupply(ctx, id)
	if err != nil {
		return nil, err
	}
	hasSKU, err := s.repo.catalogHasSKU(ctx, id, false)
	if err != nil {
		return nil, err
	}
	if hasSKU {
		return nil, errors.New("supplier.PRODUCT_NOT_FOUND")
	}
	return p, nil
}
