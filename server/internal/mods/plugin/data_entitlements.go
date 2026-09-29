package plugin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	lp "github.com/NovaWorks/zcard-next/server/internal/mods/license/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	pl "github.com/NovaWorks/zcard-next/server/internal/platform/pluginlicense"
)

// All fields below are protected by coordinator.mu once serving begins.
type licensePolicy struct {
	identity                   lp.InstanceIdentity
	instanceID, issuer, domain string
	roots                      map[string]pl.Root
	state                      pl.State
	ready                      bool
	now                        func() time.Time
}

func newLicensePolicy(cfg *conf.Data, identity lp.InstanceIdentity) (*licensePolicy, error) {
	domain, err := pl.Domain(cfg.PluginLicenseDomain)
	if err != nil || len(cfg.PluginLicenseRoots) > pl.MaxIssuers {
		return nil, fmt.Errorf("invalid plugin licensing configuration")
	}
	roots := map[string]pl.Root{}
	for id, r := range cfg.PluginLicenseRoots {
		if r == nil || !pluginIDPattern.MatchString(id) || len(id) > 64 || !pl.ValidIssuer(r.Issuer) {
			return nil, fmt.Errorf("invalid plugin license trust root")
		}
		key, err := base64.StdEncoding.Strict().DecodeString(r.PublicKey)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("invalid plugin license public key")
		}
		roots[id] = pl.Root{Issuer: r.Issuer, PublicKey: key}
	}
	if cfg.PluginLicenseIssuer != "" && !pl.ValidIssuer(cfg.PluginLicenseIssuer) {
		return nil, fmt.Errorf("invalid plugin license issuer")
	}
	return &licensePolicy{identity: identity, issuer: cfg.PluginLicenseIssuer, domain: domain, roots: roots, state: pl.NewState(), now: time.Now}, nil
}
func (m *Manager) loadLicensing(ctx context.Context) error {
	m.coordinator.mu.Lock()
	defer m.coordinator.mu.Unlock()
	p := m.licensing
	if p == nil {
		return nil
	}
	p.ready = false
	id, err := p.identity.InstanceID(ctx)
	if err != nil {
		return err
	}
	p.instanceID = id
	row, err := data.Client(ctx, m.repo.data).Setting.Query().Where(setting.Group("plugin_entitlements"), setting.Key("state")).Only(ctx)
	if ent.IsNotFound(err) {
		p.state = pl.NewState()
		p.ready = true
		return nil
	}
	if err != nil {
		return err
	}
	// Reauthenticate persisted signed documents; changing the deployment's
	// trust or identity never turns an old database record into an authorization.
	var persisted pl.State
	if len(row.Value) > 2<<20 || json.Unmarshal(row.Value, &persisted) != nil || persisted.Licenses == nil || persisted.Revocations == nil || len(persisted.Licenses) > pl.MaxLicenses || len(persisted.Revocations) > pl.MaxIssuers {
		return fmt.Errorf("invalid persisted plugin security state")
	}
	next := pl.NewState()
	for k, e := range persisted.Licenses {
		var err error
		next, err = next.Accept(e, p.roots, id, p.domain, p.now())
		if err != nil {
			return fmt.Errorf("persisted plugin license authentication failed: %w", err)
		}
		if _, ok := next.Licenses[k]; !ok {
			return fmt.Errorf("invalid persisted entitlement identity")
		}
	}
	for k, e := range persisted.Revocations {
		var err error
		next, err = next.AcceptRevocations(e, p.roots, p.now())
		if err != nil || k != e.List.Issuer {
			return fmt.Errorf("persisted plugin safety policy authentication failed")
		}
	}
	p.state = next
	p.ready = true
	return nil
}
func (m *Manager) licenseReason(a port.Artifact) pl.Reason {
	p := m.licensing
	if p == nil || !p.ready {
		if a.Manifest.Entitlement.Mode == "free" && p == nil {
			return pl.Valid
		}
		return pl.Missing
	}
	return p.state.Check(p.issuer, p.instanceID, a.Manifest.ID, a.Manifest.Version, a.Descriptor.ArchiveSHA256, a.Manifest.Entitlement.Mode, p.now())
}
func (m *Manager) runtimeLicenseOK(rt port.PreparedRuntime) bool {
	a, ok := rt.(*artifactRuntime)
	if !ok {
		return true
	} // Test/custom adapters have no signed plugin manifest.
	return m.licenseReason(port.Artifact{Manifest: a.manifest, Descriptor: pc.ArtifactDescriptor{ArchiveSHA256: a.digest}}) == pl.Valid
}

// installSecurity does not enable plugins, reprepare runtimes, clear faults or
// change rules. Renewal only changes what future leases may acquire.
func (m *Manager) installSecurity(ctx context.Context, raw []byte, safety bool, actor port.Actor) error {
	if (!actor.InstanceAdmin || !actor.ScopeVerified || actor.SubsiteID != 0 || (actor.AdminID == 0 && !actor.LocalOperator)) || m.coordinator.readOnly {
		return contractError(pc.Forbidden, "instance administrator required")
	}
	m.coordinator.mu.Lock()
	defer m.coordinator.mu.Unlock()
	p := m.licensing
	if p == nil || !p.ready {
		return contractError(pc.Unavailable, "licensing is not initialized")
	}
	next := p.state
	var err error
	var id, revision string
	if safety {
		var e pl.RevocationEnvelope
		if err = pl.Decode(raw, &e); err != nil {
			return err
		}
		next, err = p.state.AcceptRevocations(e, p.roots, p.now())
		revision = e.List.Sequence
	} else {
		var e pl.Envelope
		if err = pl.Decode(raw, &e); err != nil {
			return err
		}
		next, err = p.state.Accept(e, p.roots, p.instanceID, p.domain, p.now())
		id, revision = e.License.PluginID, e.License.Revision
	}
	if err != nil {
		return err
	}
	b, err := json.Marshal(next)
	if err != nil || len(b) > 2<<20 {
		return pl.ErrInvalid
	}
	if err = data.Client(ctx, m.repo.data).Setting.Create().SetGroup("plugin_entitlements").SetKey("state").SetValue(b).OnConflict(sql.ConflictColumns(setting.FieldGroup, setting.FieldKey)).UpdateValue().Exec(ctx); err != nil {
		return err
	}
	p.state = next
	n, _ := pl.Revision(revision)
	action := "plugin.license_install"
	if safety {
		action = "plugin.safety_update"
	}
	m.repo.auditRule(ctx, actor, action, port.RuleKey{PluginID: id}, n)
	return nil
}

type entitlementView struct {
	PluginID  string `json:"pluginId"`
	Issuer    string `json:"issuer"`
	Revision  string `json:"revision"`
	ExpiresAt int64  `json:"expiresAt"`
	Status    string `json:"status"`
}

func (m *Manager) entitlementStatus() any {
	m.coordinator.mu.Lock()
	defer m.coordinator.mu.Unlock()
	p := m.licensing
	if p == nil || !p.ready {
		return map[string]any{"ready": false}
	}
	rows := make([]entitlementView, 0, len(p.state.Licenses))
	for _, e := range p.state.Licenses {
		l := e.License
		status := l.Status
		if l.Status == "active" {
			if p.now().Unix() >= l.ExpiresAt {
				status = string(pl.Expired)
			} else if p.now().Unix() < l.NotBefore {
				status = string(pl.NotYetValid)
			}
		}
		if l.Issuer != p.issuer {
			status = string(pl.Incompatible)
		}
		rows = append(rows, entitlementView{l.PluginID, l.Issuer, l.Revision, l.ExpiresAt, status})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].PluginID == rows[j].PluginID {
			return rows[i].Issuer < rows[j].Issuer
		}
		return rows[i].PluginID < rows[j].PluginID
	})
	return map[string]any{"ready": true, "instanceId": p.instanceID, "issuer": p.issuer, "licenses": rows, "purchaseAvailable": false}
}
