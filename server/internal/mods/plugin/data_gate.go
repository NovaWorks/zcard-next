package plugin

import (
	"context"
	"strconv"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/NovaWorks/zcard-next/server/internal/platform/pluginruntime"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

type RequiredGate struct{ repo *Repo }

func NewRequiredGate(r *Repo) *RequiredGate { return &RequiredGate{repo: r} }

type purchaseSession struct {
	repo      *Repo
	leases    map[string]port.RuntimeLease
	digests   map[string]string
	versions  map[string]string
	remaining time.Duration
}

func (g *RequiredGate) Begin(ctx context.Context) (port.PurchaseSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c := g.repo.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()
	s := &purchaseSession{repo: g.repo, leases: map[string]port.RuntimeLease{}, digests: map[string]string{}, versions: map[string]string{}, remaining: pluginruntime.RequestBudget}
	for id, slot := range c.slots {
		if c.licenseCheck != nil && !c.licenseCheck(slot.runtime) {
			continue
		}
		slot.refs++
		s.leases[id] = &lease{owner: c, slot: slot}
		if rt, ok := slot.runtime.(*artifactRuntime); ok {
			s.digests[id] = rt.digest
			s.versions[id] = rt.manifest.Version
		}
	}
	return s, nil
}
func (s *purchaseSession) Release() {
	for _, l := range s.leases {
		l.Release()
	}
}
func decimal(n uint64) pc.Decimal { return pc.Decimal(strconv.FormatUint(n, 10)) }
func (s *purchaseSession) Check(ctx context.Context, in port.PurchaseInput) ([]map[string]any, error) {
	tc, err := tenancy.Require(ctx)
	if err != nil || tc.SubsiteID != in.SubsiteID {
		return nil, contractError(pc.Forbidden, "explicit purchase scope required")
	}
	if len(in.Items) == 0 || len(in.Items) > pluginruntime.MaxItems {
		return nil, contractError(pc.InvalidContract, "purchase item limit")
	}
	ids := make([]uint64, 0, len(in.Items))
	items := map[uint64][]port.PurchaseItem{}
	for _, it := range in.Items {
		ids = append(ids, it.ProductID)
		items[it.ProductID] = append(items[it.ProductID], it)
	}
	started := time.Now()
	defer func() { s.remaining -= time.Since(started) }()
	budget, cancel := context.WithTimeout(ctx, s.remaining)
	defer cancel()
	reqs, err := s.repo.ListForProducts(budget, in.SubsiteID, ids)
	if err != nil {
		return nil, contractError(pc.Unavailable, "requirement read failed")
	}
	var decisions []map[string]any
	for _, req := range reqs {
		if !req.Required {
			continue
		}
		if in.Channel != "storefront" {
			return nil, &GateError{Reason: pc.SupplyRestricted}
		}
		l := s.leases[req.Key.PluginID]
		if l == nil {
			return nil, contractError(pc.Unavailable, "required runtime missing")
		}
		rule, e := s.repo.rule(budget, req.Key)
		if e != nil || !rule.Config.Enabled || rule.Requirement.Revision != req.Revision {
			return nil, contractError(pc.Unavailable, "required configuration unavailable")
		}
		for _, it := range items[req.Key.ProductID] {
			input := pc.Input{SchemaVersion: 1, Hook: pc.HookOrderPreCreate, PluginID: req.Key.PluginID, Generation: decimal(l.Generation()), SubsiteID: decimal(in.SubsiteID), ProductID: decimal(it.ProductID), SKUID: decimal(it.SKUID), Quantity: int(it.Quantity), Channel: in.Channel, Member: pc.Member{Authenticated: in.UserID > 0, EffectiveLevelID: decimal(in.LevelID)}, Config: rule.Config}
			out, e := l.Evaluate(budget, input)
			if e != nil {
				return nil, contractError(pc.Unavailable, "required hook failed")
			}
			if !out.Allow {
				return nil, &GateError{Reason: out.Reason}
			}
			decisions = append(decisions, map[string]any{"plugin_id": req.Key.PluginID, "digest": s.digests[req.Key.PluginID], "version": s.versions[req.Key.PluginID], "generation": string(input.Generation), "config_revision": string(rule.Config.Revision), "requirement_revision": strconv.FormatUint(req.Revision, 10), "product_id": string(input.ProductID), "allow": out.Allow, "reason": string(out.Reason)})
		}
	}
	return decisions, nil
}

// GateError exposes only the frozen public denial code, never level IDs.
type GateError struct{ Reason pc.Reason }

func (e *GateError) Error() string { return string(e.Reason) }

var _ port.PurchaseGate = (*RequiredGate)(nil)
