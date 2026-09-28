package plugin

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/installedplugin"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/plugindata"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pluginrequirement"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pluginrulelevelref"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	auditport "github.com/NovaWorks/zcard-next/server/internal/mods/audit/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

type Repo struct {
	data        *data.Data
	coordinator *Coordinator
	audit       auditport.Auditor
}

func NewRepo(d *data.Data, c *Coordinator, a auditport.Auditor) *Repo {
	return &Repo{data: d, coordinator: c, audit: a}
}
func validRuleKey(k port.RuleKey) bool {
	return len(k.PluginID) <= 64 && pluginIDPattern.MatchString(k.PluginID) && k.ProductID > 0
}
func (r *Repo) scope(ctx context.Context, k port.RuleKey) error {
	tc, err := tenancy.Require(ctx)
	if err != nil || !validRuleKey(k) || tc.SubsiteID != k.SubsiteID {
		return contractError(pc.Forbidden, "explicit product scope required")
	}
	return nil
}
func (r *Repo) actor(ctx context.Context, k port.RuleKey, a port.Actor) error {
	if err := r.scope(ctx, k); err != nil {
		return err
	}
	if (a.AdminID == 0 && !a.LocalOperator) || !a.ScopeVerified || a.SubsiteID != k.SubsiteID {
		return contractError(pc.Forbidden, "invalid actor scope")
	}
	return nil
}
func (r *Repo) product(ctx context.Context, k port.RuleKey) error {
	exists, err := data.Client(ctx, r.data).Product.Query().Where(product.ID(k.ProductID), product.SubsiteID(k.SubsiteID), product.StatusGTE(0)).Exist(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return contractError(pc.Forbidden, "product outside editable scope")
	}
	return nil
}
func defaultRule(k port.RuleKey) port.Rule {
	return port.Rule{Requirement: port.Requirement{Key: k}, Config: pc.Config{SchemaVersion: 1, Revision: "0", AllowedLevelIDs: []pc.Decimal{}}}
}
func (r *Repo) rule(ctx context.Context, k port.RuleKey) (port.Rule, error) {
	out := defaultRule(k)
	c := data.Client(ctx, r.data)
	req, err := c.PluginRequirement.Query().Where(pluginrequirement.PluginID(k.PluginID), pluginrequirement.SubsiteID(k.SubsiteID), pluginrequirement.ProductID(k.ProductID)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return out, err
	}
	if err == nil {
		out.Requirement.Required = req.Required
		out.Requirement.Revision = uint64(req.Revision)
	}
	row, err := c.PluginData.Query().Where(plugindata.PluginID(k.PluginID), plugindata.SubsiteID(k.SubsiteID), plugindata.EntityType("product"), plugindata.EntityID(k.ProductID), plugindata.Key("config")).Only(ctx)
	if ent.IsNotFound(err) {
		if out.Requirement.Required {
			return out, contractError(pc.Unavailable, "required config missing")
		}
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if row.SchemaVersion != 1 || pc.Validate(pc.ConfigKind, row.Payload) != nil {
		return out, contractError(pc.Unavailable, "invalid stored config")
	}
	if err = json.Unmarshal(row.Payload, &out.Config); err != nil {
		return out, err
	}
	if string(out.Config.Revision) != strconv.FormatInt(row.Revision, 10) || out.Config.Enabled != out.Requirement.Required {
		return out, contractError(pc.Unavailable, "inconsistent stored rule")
	}
	return out, nil
}
func (r *Repo) Get(ctx context.Context, k port.RuleKey) (port.Rule, error) {
	if err := r.scope(ctx, k); err != nil {
		return port.Rule{}, err
	}
	if err := r.product(ctx, k); err != nil {
		return port.Rule{}, err
	}
	return r.rule(ctx, k)
}
func (r *Repo) Save(ctx context.Context, in port.SaveConfig) (port.Rule, error) {
	if r.coordinator.readOnly {
		return port.Rule{}, contractError(pc.Unavailable, "plugin configuration requires all mode")
	}
	if err := r.actor(ctx, in.Key, in.Actor); err != nil {
		return port.Rule{}, err
	}
	raw, err := json.Marshal(in.Config)
	if err != nil {
		return port.Rule{}, err
	}
	if err = pc.Validate(pc.ConfigKind, raw); err != nil {
		return port.Rule{}, err
	}
	if in.Expected.SchemaVersion != 1 || string(in.Config.Revision) != strconv.FormatUint(in.Expected.ConfigRevision, 10) {
		return port.Rule{}, contractError(pc.Conflict, "config schema or revision mismatch")
	}
	cdr := r.coordinator
	cdr.mu.Lock()
	defer cdr.mu.Unlock()
	if cdr.pending[in.Key.PluginID] {
		return port.Rule{}, contractError(pc.Conflict, "lifecycle reconciliation pending")
	}
	if in.Config.Enabled && cdr.slots[in.Key.PluginID] == nil {
		return port.Rule{}, contractError(pc.Unavailable, "purchase backend is unavailable")
	}
	var out port.Rule
	err = data.RuleWriteTx(ctx, r.data, func(ctx context.Context) error {
		p, err := data.GuardProductWrite(ctx, r.data, in.Key.ProductID)
		if err != nil {
			return err
		}
		c := data.Client(ctx, r.data)
		state, err := c.InstalledPlugin.Query().Where(installedplugin.PluginID(in.Key.PluginID), installedplugin.Uninstalled(false)).Only(ctx)
		if err != nil {
			return contractError(pc.Unavailable, "plugin is not installed")
		}
		if uint64(state.DesiredGeneration) != in.Expected.Generation || state.DesiredGeneration != state.ObservedGeneration {
			return contractError(pc.Conflict, "generation changed")
		}
		previous, err := r.rule(ctx, in.Key)
		if err != nil {
			return err
		}
		rev, _ := strconv.ParseUint(string(previous.Config.Revision), 10, 64)
		if rev != in.Expected.ConfigRevision || previous.Requirement.Revision != in.Expected.RequirementRevision {
			return contractError(pc.Conflict, "rule revision changed")
		}
		if rev >= math.MaxInt64 || previous.Requirement.Revision >= math.MaxInt64 || p.PluginRuleRevision == math.MaxInt64 {
			return contractError(pc.Conflict, "revision exhausted")
		}
		levels := make([]uint64, 0, len(in.Config.AllowedLevelIDs))
		for _, id := range in.Config.AllowedLevelIDs {
			n, _ := strconv.ParseUint(string(id), 10, 64)
			levels = append(levels, n)
		}
		sort.Slice(levels, func(i, j int) bool { return levels[i] < levels[j] })
		for _, id := range levels {
			if r.data.Dialect == db.SQLite {
				if _, err = c.MemberLevel.Update().Where(memberlevel.ID(id)).AddSort(0).Save(ctx); err != nil {
					return err
				}
			}
			q := c.MemberLevel.Query().Where(memberlevel.ID(id))
			if r.data.Dialect != db.SQLite {
				q = q.ForUpdate()
			}
			lv, err := q.Only(ctx)
			if err != nil || !lv.Enabled {
				return contractError(pc.InvalidContract, "selected level missing or disabled")
			}
		}
		cfg := in.Config
		cfg.Revision = pc.Decimal(strconv.FormatUint(rev+1, 10))
		payload, _ := json.Marshal(cfg)
		if err = r.writeConfig(ctx, in.Key, payload, 1, int64(rev+1)); err != nil {
			return err
		}
		requirement := port.Requirement{Key: in.Key, Required: cfg.Enabled, Revision: previous.Requirement.Revision + 1}
		if err = r.writeRequirement(ctx, requirement); err != nil {
			return err
		}
		if _, err = c.PluginRuleLevelRef.Delete().Where(pluginrulelevelref.PluginID(in.Key.PluginID), pluginrulelevelref.SubsiteID(in.Key.SubsiteID), pluginrulelevelref.ProductID(in.Key.ProductID)).Exec(ctx); err != nil {
			return err
		}
		if cfg.Enabled {
			for _, id := range levels {
				if _, err = c.PluginRuleLevelRef.Create().SetPluginID(in.Key.PluginID).SetSubsiteID(in.Key.SubsiteID).SetProductID(in.Key.ProductID).SetLevelID(id).Save(ctx); err != nil {
					return err
				}
			}
		}
		if err = c.Product.UpdateOneID(p.ID).AddPluginRuleRevision(1).Exec(ctx); err != nil {
			return err
		}
		out = port.Rule{Requirement: requirement, Config: cfg}
		r.auditRule(ctx, in.Actor, "plugin.config_saved", in.Key, requirement.Revision)
		return nil
	})
	return out, err
}
func (r *Repo) writeConfig(ctx context.Context, k port.RuleKey, payload []byte, schema int, revision int64) error {
	c := data.Client(ctx, r.data)
	row, err := c.PluginData.Query().Where(plugindata.PluginID(k.PluginID), plugindata.SubsiteID(k.SubsiteID), plugindata.EntityType("product"), plugindata.EntityID(k.ProductID), plugindata.Key("config")).Only(ctx)
	if ent.IsNotFound(err) {
		_, err = c.PluginData.Create().SetPluginID(k.PluginID).SetSubsiteID(k.SubsiteID).SetEntityType("product").SetEntityID(k.ProductID).SetKey("config").SetPayload(payload).SetSchemaVersion(schema).SetRevision(revision).Save(ctx)
		return err
	}
	if err != nil {
		return err
	}
	return c.PluginData.UpdateOneID(row.ID).SetPayload(payload).SetSchemaVersion(schema).SetRevision(revision).Exec(ctx)
}
func (r *Repo) writeRequirement(ctx context.Context, v port.Requirement) error {
	c := data.Client(ctx, r.data)
	row, err := c.PluginRequirement.Query().Where(pluginrequirement.PluginID(v.Key.PluginID), pluginrequirement.SubsiteID(v.Key.SubsiteID), pluginrequirement.ProductID(v.Key.ProductID)).Only(ctx)
	if ent.IsNotFound(err) {
		_, err = c.PluginRequirement.Create().SetPluginID(v.Key.PluginID).SetSubsiteID(v.Key.SubsiteID).SetProductID(v.Key.ProductID).SetRequired(v.Required).SetRevision(int64(v.Revision)).Save(ctx)
		return err
	}
	if err != nil {
		return err
	}
	return c.PluginRequirement.UpdateOneID(row.ID).SetRequired(v.Required).SetRevision(int64(v.Revision)).Exec(ctx)
}
func (r *Repo) Release(ctx context.Context, in port.ReleaseRule) (port.Requirement, error) {
	if r.coordinator.readOnly {
		return port.Requirement{}, contractError(pc.Unavailable, "plugin configuration requires all mode")
	}
	if err := r.actor(ctx, in.Key, in.Actor); err != nil {
		return port.Requirement{}, err
	}
	r.coordinator.mu.Lock()
	defer r.coordinator.mu.Unlock()
	var out port.Requirement
	err := data.RuleWriteTx(ctx, r.data, func(ctx context.Context) error {
		p, err := data.GuardProductWrite(ctx, r.data, in.Key.ProductID)
		if err != nil {
			return err
		}
		c := data.Client(ctx, r.data)
		req, err := c.PluginRequirement.Query().Where(pluginrequirement.PluginID(in.Key.PluginID), pluginrequirement.SubsiteID(in.Key.SubsiteID), pluginrequirement.ProductID(in.Key.ProductID)).Only(ctx)
		var revision int64
		if err == nil {
			revision = req.Revision
		} else if !ent.IsNotFound(err) {
			return err
		}
		if uint64(revision) != in.ExpectedRequirementRevision {
			return contractError(pc.Conflict, "requirement revision changed")
		}
		row, err := c.PluginData.Query().Where(plugindata.PluginID(in.Key.PluginID), plugindata.SubsiteID(in.Key.SubsiteID), plugindata.EntityType("product"), plugindata.EntityID(in.Key.ProductID), plugindata.Key("config")).Only(ctx)
		var cr int64
		if err == nil {
			cr = row.Revision
		} else if !ent.IsNotFound(err) {
			return err
		}
		if revision == math.MaxInt64 || cr == math.MaxInt64 || p.PluginRuleRevision == math.MaxInt64 {
			return contractError(pc.Conflict, "revision exhausted")
		}
		cfg := defaultRule(in.Key).Config
		cfg.Revision = pc.Decimal(strconv.FormatInt(cr+1, 10))
		raw, _ := json.Marshal(cfg)
		if err = r.writeConfig(ctx, in.Key, raw, 1, cr+1); err != nil {
			return err
		}
		out = port.Requirement{Key: in.Key, Revision: uint64(revision + 1)}
		if err = r.writeRequirement(ctx, out); err != nil {
			return err
		}
		if _, err = c.PluginRuleLevelRef.Delete().Where(pluginrulelevelref.PluginID(in.Key.PluginID), pluginrulelevelref.SubsiteID(in.Key.SubsiteID), pluginrulelevelref.ProductID(in.Key.ProductID)).Exec(ctx); err != nil {
			return err
		}
		if err = c.Product.UpdateOneID(p.ID).AddPluginRuleRevision(1).Exec(ctx); err != nil {
			return err
		}
		r.auditRule(ctx, in.Actor, "plugin.rule_released", in.Key, out.Revision)
		return nil
	})
	return out, err
}
func (r *Repo) ListForProducts(ctx context.Context, subsite uint64, ids []uint64) ([]port.Requirement, error) {
	tc, err := tenancy.Require(ctx)
	if err != nil || tc.SubsiteID != subsite {
		return nil, contractError(pc.Forbidden, "explicit purchase scope required")
	}
	if len(ids) == 0 {
		return []port.Requirement{}, nil
	}
	if len(ids) > 1000 {
		return nil, contractError(pc.InvalidContract, "too many products")
	}
	rows, err := data.Client(ctx, r.data).PluginRequirement.Query().Where(pluginrequirement.SubsiteID(subsite), pluginrequirement.ProductIDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]port.Requirement, 0, len(rows))
	for _, row := range rows {
		out = append(out, port.Requirement{Key: port.RuleKey{SubsiteID: subsite, ProductID: row.ProductID, PluginID: row.PluginID}, Required: row.Required, Revision: uint64(row.Revision)})
	}
	return out, nil
}
func (r *Repo) LevelReferenced(ctx context.Context, id uint64) (bool, error) {
	return data.Client(ctx, r.data).PluginRuleLevelRef.Query().Where(pluginrulelevelref.LevelID(id)).Exist(ctx)
}
func (r *Repo) auditRule(ctx context.Context, a port.Actor, action string, k port.RuleKey, rev uint64) {
	if r.audit != nil {
		r.audit.Security(ctx, auditport.SecurityEntry{ActorType: actorType(a), ActorID: a.AdminID, Action: action, Metadata: map[string]any{"plugin_id": k.PluginID, "subsite_id": k.SubsiteID, "product_id": k.ProductID, "revision": rev}})
	}
}

var _ port.ConfigStore = (*Repo)(nil)
var _ port.RequirementStore = (*Repo)(nil)

func actorType(a port.Actor) string {
	if a.LocalOperator {
		return "system"
	}
	return "admin"
}
