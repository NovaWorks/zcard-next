package supply

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

// Validate the locally sold SKU set, not unrelated upstream variants. A known
// available variant is sufficient; unknown variants never turn into zero stock.
func (s *SyncService) listingSKUStock(ctx context.Context, a adapter.Adapter, connection uint64, p *adapter.Product) error {
	c := s.repo.entClient(ctx)
	local, err := c.Product.Query().Where(product.UpstreamSourceID(connection), product.UpstreamProductCode(p.ID), product.StatusGTE(0)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	skus, err := c.ProductSku.Query().Where(productsku.ProductID(local.ID)).Order(ent.Asc(productsku.FieldID)).All(ctx)
	if err != nil {
		return err
	}
	if p.UpstreamExtra == nil {
		p.UpstreamExtra = map[string]any{}
	}
	p.UpstreamExtra["_local_listing_skus"] = listingSKUSignature(skus)
	if len(skus) == 0 || !p.IsActive || (p.Stock < -1 && p.StockError != "") {
		return nil
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	remote := map[string]adapter.SKU{}
	for _, sk := range p.SKUs {
		code := sk.Code
		if code == "" {
			code = sk.ID
		}
		remote[code] = sk
	}
	var total int32
	unknown := false
	unlimited := false
	for _, sk := range skus {
		r, ok := remote[sk.UpstreamSkuID]
		if sk.UpstreamSkuID == "" || !ok {
			unknown = true
			continue
		}
		if !r.IsActive {
			continue
		}
		call, stop := context.WithTimeout(bounded, 8*time.Second)
		n, e := a.GetStock(call, p.ID, sk.UpstreamSkuID)
		stop()
		if errors.Is(e, adapter.ErrRateLimited) {
			p.Stock = -2
			p.StockError = "货源限流，请稍后重试"
			return e
		}
		if e != nil || n < -1 {
			unknown = true
			continue
		}
		if n == -1 {
			unlimited = true
		} else if int64(total)+int64(n) > 2147483647 {
			total = 2147483647
		} else {
			total += n
		}
	}
	if unlimited {
		p.Stock = -1
	} else if total > 0 {
		p.Stock = total
	} else if unknown {
		p.Stock = -2
		p.StockError = "本地在售规格库存未完整确认"
	} else {
		p.Stock = 0
	}
	return nil
}

func (s *SyncService) observeListing(ctx context.Context, localID uint64, p *adapter.Product) error {
	local, err := data.GuardProductWrite(ctx, s.repo.data, localID)
	if ent.IsNotFound(err) {
		return nil
	}
	// Some adapter-only tests use a writer with no backing local product.
	if err != nil {
		if localID == 0 {
			return nil
		}
		return err
	}
	return data.ObserveListing(ctx, s.repo.data, local, p.Stock, p.IsActive, stockObservationTime(p))
}

func listingSKUSignature(skus []*ent.ProductSku) string {
	rows := make([]any, 0, len(skus))
	for _, sk := range skus {
		rows = append(rows, []any{sk.ID, sk.UpstreamSkuID, sk.FulfillmentMode, sk.Price})
	}
	raw, _ := json.Marshal(rows)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// Called with the product locked, after network I/O, so a replaced/deleted SKU
// cannot cause the old result to restore a different locally sold variant set.
func (s *SyncService) validateListingSKUs(ctx context.Context, id uint64, p *adapter.Product) error {
	expected, ok := p.UpstreamExtra["_local_listing_skus"].(string)
	if !ok {
		return nil
	}
	current, e := s.repo.entClient(ctx).ProductSku.Query().Where(productsku.ProductID(id)).Order(ent.Asc(productsku.FieldID)).All(ctx)
	if e != nil {
		return e
	}
	if listingSKUSignature(current) != expected {
		p.Stock = -2
		p.StockError = "规格已变化，等待重新检查库存"
	}
	return nil
}
