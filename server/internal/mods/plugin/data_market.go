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
	ExplicitOrigin      bool                        `json:"explicitOrigin,omitempty"`
	ProfileID           string                      `json:"profileId,omitempty"`
	SafetyCheckedAt     int64                       `json:"safetyCheckedAt,omitempty"`
	Origin              string                      `json:"origin"`
	Checkpoints         map[string]marketCheckpoint `json:"checkpoints"`
	LicensedCheckpoints map[string]marketCheckpoint `json:"licensedCheckpoints,omitempty"`
	Binding             []byte                      `json:"binding,omitempty"`
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
	if out.LicensedCheckpoints == nil {
		out.LicensedCheckpoints = map[string]marketCheckpoint{}
	}
	if len(out.LicensedCheckpoints) > 16 {
		return out, fmt.Errorf("invalid licensed market history")
	}
	for _, checkpoints := range []map[string]marketCheckpoint{out.Checkpoints, out.LicensedCheckpoints} {
		for origin, v := range checkpoints {
			if _, e = mc.Origin(origin); e != nil {
				return out, e
			}
			if _, e = mc.Revision(v.Revision); e != nil || !digestPattern.MatchString(v.Hash) {
				return out, fmt.Errorf("invalid persisted market checkpoint")
			}
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
	if v.Origin != origin && len(v.Binding) != 0 {
		return contractError(pc.Conflict, "请先解除市场绑定或取消未确认的配对，再修改市场地址")
	}
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		if v.Origin != origin {
			// Mark every context locally unreadable before exposing the new origin. Ciphertexts stay for old-origin revocation.
			rows, e := data.Client(ctx, r.data).Setting.Query().Where(setting.Group("plugin_market"), setting.KeyHasPrefix("view.")).All(ctx)
			if e != nil {
				return e
			}
			// Context origin is immutable and every read checks it; persist a generation-independent revocation marker too.
			for _, row := range rows {
				if e = r.putMarketSetting(ctx, "revoked."+row.Key[5:], true); e != nil {
					return e
				}
			}
		}
		v.Origin = origin
		v.ExplicitOrigin = true
		v.ProfileID = ""
		return r.writeMarket(ctx, v)
	})
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
	checkpoints := v.Checkpoints
	if c.APIVersion == "2" {
		if v.LicensedCheckpoints == nil {
			v.LicensedCheckpoints = map[string]marketCheckpoint{}
		}
		checkpoints = v.LicensedCheckpoints
	} else if c.APIVersion != "1" {
		return fmt.Errorf("unsupported catalog version")
	}
	old, ok := checkpoints[c.Origin]
	if ok {
		previous, _ := mc.Revision(old.Revision)
		if n < previous || (n == previous && old.Hash != hash) {
			return fmt.Errorf("market catalog replay or conflicting revision")
		}
		if n == previous {
			return nil
		}
	} else if len(checkpoints) >= 16 {
		return fmt.Errorf("market origin history limit reached")
	}
	checkpoints[c.Origin] = marketCheckpoint{Revision: c.Revision, Hash: hash}
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
