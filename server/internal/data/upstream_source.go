package data

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
	"github.com/NovaWorks/zcard-next/server/internal/platform/upstreamsource"
)

func CaptureUpstreamSource(ctx context.Context, c *ent.Client, id uint64, code, sku, stage string) (map[string]any, error) {
	in := upstreamsource.Input{ConnectionID: id, Code: code, SKU: sku, Stage: stage}
	if id != 0 {
		conn, err := c.SupplyConnection.Get(ctx, id)
		if err != nil && !ent.IsNotFound(err) {
			return nil, err
		}
		if conn != nil {
			in.Name, in.Driver, in.BaseURL, in.Settings = conn.Name, conn.Driver, conn.BaseURL, conn.Settings
		}
	}
	return upstreamsource.Resolve(in), nil
}

// CurrentProductSources is only for product administration, never historical orders.
func CurrentProductSources(ctx context.Context, c *ent.Client, ids []uint64) (map[uint64]map[string]any, error) {
	out := map[uint64]map[string]any{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.Product.Query().Where(product.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	connIDs := []uint64{}
	for _, p := range rows {
		if p.UpstreamSourceID != 0 {
			connIDs = append(connIDs, p.UpstreamSourceID)
		}
	}
	connections := map[uint64]*ent.SupplyConnection{}
	if len(connIDs) > 0 {
		conns, e := c.SupplyConnection.Query().Where(supplyconnection.IDIn(connIDs...)).All(ctx)
		if e != nil {
			return nil, e
		}
		for _, conn := range conns {
			connections[conn.ID] = conn
		}
	}
	for _, p := range rows {
		in := upstreamsource.Input{ConnectionID: p.UpstreamSourceID, Code: p.UpstreamProductCode, Stage: "current"}
		if conn := connections[p.UpstreamSourceID]; conn != nil {
			in.Name, in.Driver, in.BaseURL, in.Settings = conn.Name, conn.Driver, conn.BaseURL, conn.Settings
		}
		out[p.ID] = upstreamsource.Resolve(in)
	}
	return out, nil
}

// ProcurementSources recovers legacy records by their original connection and
// product code. It deliberately does not consult today's product/SKU binding.
func ProcurementSources(ctx context.Context, c *ent.Client, rows []*ent.ProcurementOrder) (map[uint64]map[string]any, error) {
	out := map[uint64]map[string]any{}
	ids, connIDs := []uint64{}, []uint64{}
	for _, po := range rows {
		if len(po.UpstreamSourceSnapshot) > 0 {
			out[po.ID] = po.UpstreamSourceSnapshot
		} else {
			ids = append(ids, po.ID)
			connIDs = append(connIDs, po.ConnectionID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	items, err := c.ProcurementItem.Query().Where(procurementitem.ProcurementIDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	codes := map[uint64]string{}
	for _, it := range items {
		codes[it.ProcurementID] = it.UpstreamSku
	}
	conns, err := c.SupplyConnection.Query().Where(supplyconnection.IDIn(connIDs...)).All(ctx)
	if err != nil {
		return nil, err
	}
	connections := map[uint64]*ent.SupplyConnection{}
	for _, conn := range conns {
		connections[conn.ID] = conn
	}
	for _, po := range rows {
		if out[po.ID] != nil {
			continue
		}
		in := upstreamsource.Input{ConnectionID: po.ConnectionID, Code: codes[po.ID], Stage: "legacy"}
		if conn := connections[po.ConnectionID]; conn != nil {
			in.Name, in.Driver, in.BaseURL, in.Settings = conn.Name, conn.Driver, conn.BaseURL, conn.Settings
		}
		out[po.ID] = upstreamsource.Resolve(in)
	}
	return out, nil
}

func OrderItemSources(ctx context.Context, c *ent.Client, items []*ent.OrderItem) (map[uint64]map[string]any, error) {
	out := map[uint64]map[string]any{}
	ids := []uint64{}
	for _, it := range items {
		ids = append(ids, it.ID)
		if len(it.UpstreamSourceSnapshot) > 0 {
			out[it.ID] = it.UpstreamSourceSnapshot
		} else if it.FulfillmentType == "upstream" {
			out[it.ID] = map[string]any{"kind": "unknown", "stage": "unavailable", "link_status": "unavailable"}
		} else {
			out[it.ID] = map[string]any{"kind": "self", "stage": "legacy", "link_status": "self"}
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.ProcurementOrder.Query().Where(procurementorder.OrderItemIDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	sources, err := ProcurementSources(ctx, c, rows)
	if err != nil {
		return nil, err
	}
	for _, po := range rows {
		out[po.OrderItemID] = sources[po.ID]
	}
	return out, nil
}
