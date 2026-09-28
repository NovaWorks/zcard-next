package plugin

import (
	"context"
	"encoding/json"
	"fmt"

	"entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pluginoperation"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	mc "github.com/NovaWorks/zcard-next/server/internal/platform/marketcontract"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
)

type marketCheckpoint struct {
	Revision pc.Decimal `json:"revision"`
	Hash     string     `json:"hash"`
}
type marketState struct {
	Origin      string                      `json:"origin"`
	Checkpoints map[string]marketCheckpoint `json:"checkpoints"`
}

func (r *Repo) readMarket(ctx context.Context) (marketState, error) {
	out := marketState{Checkpoints: map[string]marketCheckpoint{}}
	row, e := data.Client(ctx, r.data).Setting.Query().Where(setting.Group("plugin_market"), setting.Key("state")).Only(ctx)
	if ent.IsNotFound(e) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	if e = json.Unmarshal(row.Value, &out); e != nil || out.Checkpoints == nil || len(out.Checkpoints) > 16 {
		return out, fmt.Errorf("invalid persisted market state")
	}
	for origin, v := range out.Checkpoints {
		if _, e = mc.Origin(origin); e != nil {
			return out, e
		}
		if _, e = mc.Revision(v.Revision); e != nil || !digestPattern.MatchString(v.Hash) {
			return out, fmt.Errorf("invalid persisted market checkpoint")
		}
	}
	return out, nil
}
func (r *Repo) writeMarket(ctx context.Context, v marketState) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return data.Client(ctx, r.data).Setting.Create().SetGroup("plugin_market").SetKey("state").SetValue(raw).OnConflict(sql.ConflictColumns(setting.FieldGroup, setting.FieldKey)).UpdateValue().Exec(ctx)
}
func (r *Repo) marketOrigin(ctx context.Context) (string, error) {
	r.marketMu.Lock()
	defer r.marketMu.Unlock()
	v, e := r.readMarket(ctx)
	return v.Origin, e
}
func (r *Repo) configureMarket(ctx context.Context, origin string) error {
	r.marketMu.Lock()
	defer r.marketMu.Unlock()
	v, e := r.readMarket(ctx)
	if e != nil {
		return e
	}
	v.Origin = origin
	return r.writeMarket(ctx, v)
}

// Caller verifies signature and freshness first; high-water marks survive restarts
// and switching origins. An older concurrent response can never lower the mark.
func (r *Repo) acceptCatalog(ctx context.Context, c mc.Catalog) error {
	r.marketMu.Lock()
	defer r.marketMu.Unlock()
	return r.acceptCatalogLocked(ctx, c)
}
func (r *Repo) acceptCatalogLocked(ctx context.Context, c mc.Catalog) error {
	v, e := r.readMarket(ctx)
	if e != nil {
		return e
	}
	if v.Origin != c.Origin {
		return fmt.Errorf("market configuration changed")
	}
	n, e := mc.Revision(c.Revision)
	if e != nil {
		return e
	}
	hash := mc.EntriesHash(c)
	old, ok := v.Checkpoints[c.Origin]
	if ok {
		previous, _ := mc.Revision(old.Revision)
		if n < previous || (n == previous && old.Hash != hash) {
			return fmt.Errorf("market catalog replay or conflicting revision")
		}
		if n == previous {
			return nil
		}
	} else if len(v.Checkpoints) >= 16 {
		return fmt.Errorf("market origin history limit reached")
	}
	v.Checkpoints[c.Origin] = marketCheckpoint{Revision: c.Revision, Hash: hash}
	return r.writeMarket(ctx, v)
}

// Existing durable operations can be recovered even while the market is offline.
func (r *Repo) completedMarketOperation(ctx context.Context, c Command) (Operation, bool, error) {
	row, err := data.Client(ctx, r.data).PluginOperation.Query().Where(pluginoperation.OperationID(c.OperationID)).Only(ctx)
	if ent.IsNotFound(err) {
		return Operation{}, false, nil
	}
	if err != nil {
		return Operation{}, false, err
	}
	if row.RequestSha256 != commandHash(c) {
		return Operation{}, false, contractError(pc.Conflict, "operation ID reused with different parameters")
	}
	v := operationOf(row)
	return v, v.Phase != port.PhasePreparing, nil
}
