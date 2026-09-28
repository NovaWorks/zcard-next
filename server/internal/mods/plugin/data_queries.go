package plugin

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pluginrequirement"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
)

type LevelOption struct {
	ID      uint64
	Name    string
	Enabled bool
}

func (r *Repo) levels(ctx context.Context) ([]LevelOption, error) {
	rows, err := data.Client(ctx, r.data).MemberLevel.Query().Order(ent.Asc(memberlevel.FieldID)).Limit(1001).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]LevelOption, 0, len(rows))
	for _, v := range rows {
		out = append(out, LevelOption{v.ID, v.Name, v.Enabled})
	}
	return out, nil
}
func (r *Repo) requirements(ctx context.Context, subsite, product uint64) ([]port.Requirement, error) {
	return r.ListForProducts(ctx, subsite, []uint64{product})
}
func (r *Repo) impact(ctx context.Context, id string, subsite uint64) (int, []uint64, error) {
	c := data.Client(ctx, r.data)
	n, err := c.PluginRequirement.Query().Where(pluginrequirement.PluginID(id), pluginrequirement.Required(true)).Count(ctx)
	if err != nil {
		return 0, nil, err
	}
	rows, err := c.PluginRequirement.Query().Where(pluginrequirement.PluginID(id), pluginrequirement.Required(true), pluginrequirement.SubsiteID(subsite)).Order(ent.Asc(pluginrequirement.FieldProductID)).Limit(1001).Select(pluginrequirement.FieldProductID).All(ctx)
	if err != nil {
		return 0, nil, err
	}
	ids := make([]uint64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ProductID)
	}
	return n, ids, nil
}

func (r *Repo) hasOrderFingerprints(ctx context.Context) (bool, error) {
	return data.Client(ctx, r.data).Order.Query().Where(order.RequestFingerprintNEQ("")).Exist(ctx)
}

func (r *Repo) hasRequirements(ctx context.Context) (bool, error) {
	return data.Client(ctx, r.data).PluginRequirement.Query().Where(pluginrequirement.Required(true)).Exist(ctx)
}
