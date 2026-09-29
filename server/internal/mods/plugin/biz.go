package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	ma "github.com/NovaWorks/zcard-next/server/internal/platform/marketaccount"
	"io"
	"math"
	"regexp"
	"slices"

	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/NovaWorks/zcard-next/server/internal/platform/pluginstorage"
)

var operationPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Command struct {
	OperationID        string     `json:"operation_id"`
	PluginID           string     `json:"plugin_id"`
	Action             string     `json:"action"`
	TargetDigest       string     `json:"target_digest"`
	ExpectedGeneration uint64     `json:"expected_generation,string"`
	ApprovedScopes     []string   `json:"approved_scopes"`
	Actor              port.Actor `json:"actor"`
	ConfirmPaid        bool       `json:"confirm_paid,omitempty"`
}
type Operation struct {
	Command          Command    `json:"command"`
	TargetGeneration uint64     `json:"target_generation,string"`
	Phase            port.Phase `json:"phase"`
	FailureCode      string     `json:"failure_code"`
}
type Status struct {
	State          port.State `json:"state"`
	DesiredDigest  string     `json:"desired_digest"`
	ObservedDigest string     `json:"observed_digest"`
	ApprovedScopes []string   `json:"approved_scopes"`
	Uninstalled    bool       `json:"uninstalled"`
}
type Manager struct {
	officialProfile *ma.PublicProfile
	repo            *Repo
	packages        port.PackageStore
	loader          port.RuntimeLoader
	coordinator     *Coordinator
	licensing       *licensePolicy
	bindingBox      *crypto.Box
}

func NewManager(repo *Repo, packages port.PackageStore, loader port.RuntimeLoader) *Manager {
	m := &Manager{repo: repo, packages: packages, loader: loader, coordinator: repo.coordinator}
	repo.coordinator.licenseCheck = m.runtimeLicenseOK
	return m
}
func validateCommand(c Command) error {
	if !operationPattern.MatchString(c.OperationID) || len(c.PluginID) > 64 || !pluginIDPattern.MatchString(c.PluginID) || c.ExpectedGeneration >= math.MaxInt64 {
		return contractError(pc.InvalidContract, "invalid operation identity or generation")
	}
	if (c.Actor.AdminID == 0 && !c.Actor.LocalOperator) || !c.Actor.ScopeVerified || !c.Actor.InstanceAdmin || c.Actor.SubsiteID != 0 {
		return contractError(pc.Forbidden, "instance administrator required")
	}
	switch c.Action {
	case "import", "enable", "upgrade", "rollback":
		if !digestPattern.MatchString(c.TargetDigest) {
			return contractError(pc.InvalidContract, "target digest required")
		}
	case "disable", "uninstall":
		if c.TargetDigest != "" {
			return contractError(pc.InvalidContract, "disable/uninstall has no target digest")
		}
	default:
		return contractError(pc.InvalidContract, "unknown lifecycle action")
	}
	return nil
}
func (m *Manager) Import(ctx context.Context, c Command, descriptor, signature []byte, archive io.Reader) (Operation, error) {
	if m.coordinator.readOnly {
		return Operation{}, contractError(pc.Unavailable, "plugin imports require single-process all mode")
	}
	if err := validateCommand(c); err != nil {
		return Operation{}, err
	}
	if c.Action != "import" && c.Action != "upgrade" && c.Action != "rollback" {
		return Operation{}, contractError(pc.InvalidContract, "upload action must be import, upgrade or rollback")
	}
	if err := m.prunePackages(ctx, c.TargetDigest); err != nil {
		return Operation{}, err
	}
	a, err := m.packages.Stage(ctx, descriptor, signature, archive)
	if err != nil {
		return Operation{}, err
	}
	if a.Manifest.ID != c.PluginID || a.Descriptor.ArchiveSHA256 != c.TargetDigest {
		return Operation{}, contractError(pc.InvalidContract, "import identity mismatch")
	}
	return m.Operate(ctx, c)
}
func commandHash(c Command) string {
	c.ApprovedScopes = slices.Clone(c.ApprovedScopes)
	slices.Sort(c.ApprovedScopes)
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (m *Manager) Operate(ctx context.Context, c Command) (Operation, error) {
	if m.coordinator.readOnly {
		return Operation{}, contractError(pc.Unavailable, "plugin lifecycle requires single-process all mode")
	}
	if err := validateCommand(c); err != nil {
		return Operation{}, err
	}
	cd := m.coordinator
	cd.mu.Lock()
	var closeOld port.PreparedRuntime
	defer func() {
		cd.mu.Unlock()
		if closeOld != nil {
			_ = closeOld.Close()
		}
	}()
	op, existing, err := m.repo.begin(ctx, c, commandHash(c))
	if err != nil {
		return Operation{}, err
	}
	if existing && op.Phase != port.PhasePreparing {
		if op.Phase == port.PhaseReady || op.Phase == port.PhasePublished {
			return m.reconcileLocked(ctx, c.PluginID)
		}
		if op.FailureCode != "" {
			return op, contractError(pc.ErrorCode(op.FailureCode), "previous operation failed")
		}
		return op, nil
	}
	fail := func(e error) (Operation, error) {
		code := pc.Unavailable
		var ce *pc.Error
		if errors.As(e, &ce) {
			code = ce.Code
		}
		op.Phase = port.PhaseFailed
		op.FailureCode = string(code)
		if saveErr := m.repo.fail(ctx, op); saveErr != nil {
			return op, fmt.Errorf("operation failure persistence: %w", saveErr)
		}
		return op, e
	}
	old, err := m.repo.status(ctx, c.PluginID)
	if err != nil {
		return fail(err)
	}
	if old.State.DesiredGeneration != c.ExpectedGeneration || ((cd.pending[c.PluginID] || old.State.ReconciliationPending) && c.Action != "disable" && c.Action != "uninstall") {
		return fail(contractError(pc.Conflict, "generation changed or reconciliation pending"))
	}
	if c.Action == "import" && old.State.DesiredGeneration > 0 && !old.Uninstalled {
		return fail(contractError(pc.Conflict, "use upgrade for an installed plugin"))
	}
	if c.Action != "import" && (old.State.DesiredGeneration == 0 || old.Uninstalled) {
		return fail(contractError(pc.Unavailable, "plugin is not installed"))
	}
	desired := old.State.DesiredEnabled
	sha := c.TargetDigest
	switch c.Action {
	case "import":
		desired = false
	case "enable":
		desired = true
	case "disable", "uninstall":
		desired = false
		sha = old.DesiredDigest
	}
	if desired {
		for _, reason := range old.State.BlockReasons {
			if reason == port.BlockExpired || reason == port.BlockRevoked {
				return fail(contractError(pc.Unavailable, "entitlement is blocked"))
			}
		}
	}
	var prepared port.PreparedRuntime
	if c.Action != "disable" && c.Action != "uninstall" {
		a, reader, e := m.packages.Open(ctx, sha)
		if e != nil {
			return fail(e)
		}
		_ = reader.Close()
		if a.Manifest.ID != c.PluginID {
			return fail(contractError(pc.InvalidContract, "artifact belongs to another plugin"))
		}
		if reason := m.licenseReason(a); reason != "" {
			return fail(contractError(pc.Unavailable, string(reason)))
		}
		if a.Manifest.Entitlement.Mode == "paid" && sha != old.DesiredDigest && !c.ConfirmPaid {
			return fail(contractError(pc.Forbidden, "explicit paid artifact confirmation required"))
		}
		if !sameScopes(c.ApprovedScopes, a.Manifest.Scopes) {
			return fail(contractError(pc.Forbidden, "explicit signed scopes approval required"))
		}
		if c.Action == "enable" && sha != old.DesiredDigest {
			return fail(contractError(pc.Conflict, "enable must reference installed artifact"))
		}
		if desired {
			if m.loader == nil {
				return fail(contractError(pc.Unavailable, "runtime backend not available"))
			}
			prepared, err = m.loader.Prepare(ctx, a)
			if err != nil {
				return fail(err)
			}
		}
	}
	op.TargetGeneration = c.ExpectedGeneration + 1
	op.Command.TargetDigest = sha
	op.Phase = port.PhaseReady
	if err = m.repo.intent(ctx, op, desired, c.Action == "uninstall"); err != nil {
		if prepared != nil {
			_ = prepared.Close()
		}
		return fail(err)
	}
	cd.pending[c.PluginID] = true
	closeOld = cd.publishLocked(c.PluginID, op.TargetGeneration, prepared)
	op.Phase = port.PhasePublished
	if err = m.repo.confirm(ctx, op); err != nil {
		return op, contractError(pc.Unavailable, "target published; persistence reconciliation pending")
	}
	delete(cd.pending, c.PluginID)
	op.Phase = port.PhaseReconciled
	return op, nil
}
func sameScopes(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}
func (m *Manager) Status(ctx context.Context, id string) (Status, error) {
	m.coordinator.mu.Lock()
	defer m.coordinator.mu.Unlock()
	s, err := m.repo.status(ctx, id)
	if err != nil {
		return s, err
	}
	if slot := m.coordinator.slots[id]; slot != nil {
		if a, ok := slot.runtime.(*artifactRuntime); ok {
			if reason := m.licenseReason(port.Artifact{Manifest: a.manifest, Descriptor: pc.ArtifactDescriptor{ArchiveSHA256: a.digest}}); reason != "" {
				s.State.BlockReasons = append(s.State.BlockReasons, port.BlockReason(reason))
			}
		}
		if health, ok := slot.runtime.(interface{ Faulted() bool }); ok && health.Faulted() {
			s.State.BlockReasons = append(s.State.BlockReasons, port.BlockRuntime)
		}
	}
	gen, published := m.coordinator.observed[id]
	s.State.ObservedGeneration = gen
	s.ObservedDigest = ""
	if published && gen == s.State.DesiredGeneration {
		s.ObservedDigest = s.DesiredDigest
	}
	s.State.ReconciliationPending = s.State.ReconciliationPending || m.coordinator.pending[id]
	if s.State.ReconciliationPending && published && gen == s.State.DesiredGeneration {
		s.State.Phase = port.PhasePublished
	}
	return s, nil
}

// Reconcile is called before the server accepts requests, or to finish a timed
// out operation. Missing artifacts/runtime retain durable requirements.
func (m *Manager) Reconcile(ctx context.Context) error {
	m.coordinator.mu.Lock()
	defer m.coordinator.mu.Unlock()
	ids, err := m.repo.installedIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = m.reconcileLocked(ctx, id); err != nil {
			var ce *pc.Error
			if !errors.As(err, &ce) {
				return err
			}
		}
	}
	return nil
}
func (m *Manager) reconcileLocked(ctx context.Context, id string) (Operation, error) {
	state, err := m.repo.status(ctx, id)
	if err != nil {
		return Operation{}, err
	}
	op, err := m.repo.operation(ctx, state.State.OperationID)
	if err != nil {
		return op, err
	}
	if slot := m.coordinator.slots[id]; slot != nil && slot.generation == state.State.DesiredGeneration {
		if err = m.repo.confirm(ctx, op); err != nil {
			return op, err
		}
		delete(m.coordinator.pending, id)
		op.Phase = port.PhaseReconciled
		return op, nil
	}
	var rt port.PreparedRuntime
	m.coordinator.pending[id] = true
	if state.State.DesiredEnabled {
		for _, reason := range state.State.BlockReasons {
			if reason == port.BlockExpired || reason == port.BlockRevoked {
				return op, contractError(pc.Unavailable, "entitlement is blocked")
			}
		}
	}
	if !state.Uninstalled {
		a, reader, e := m.packages.Open(ctx, state.DesiredDigest)
		if e != nil {
			if saveErr := m.repo.block(ctx, id, string(port.BlockMissing)); saveErr != nil {
				return op, saveErr
			}
			return op, contractError(pc.Unavailable, "artifact unavailable during recovery")
		}
		_ = reader.Close()
		if a.Manifest.ID != id || !sameScopes(state.ApprovedScopes, a.Manifest.Scopes) {
			if e := m.repo.block(ctx, id, string(port.BlockIncompatible)); e != nil {
				return op, e
			}
			return op, contractError(pc.Unavailable, "stored identity or scopes are incompatible")
		}
		if state.State.DesiredEnabled {
			if m.loader == nil {
				if saveErr := m.repo.block(ctx, id, string(port.BlockRuntime)); saveErr != nil {
					return op, saveErr
				}
				return op, contractError(pc.Unavailable, "runtime unavailable during recovery")
			}
			rt, err = m.loader.Prepare(ctx, a)
			if err != nil {
				if saveErr := m.repo.block(ctx, id, string(port.BlockRuntime)); saveErr != nil {
					return op, saveErr
				}
				return op, contractError(pc.Unavailable, "runtime failed during recovery")
			}
		}
	}
	if old := m.coordinator.publishLocked(id, state.State.DesiredGeneration, rt); old != nil {
		_ = old.Close()
	}
	op.TargetGeneration = state.State.DesiredGeneration
	op.Phase = port.PhasePublished
	if err = m.repo.confirm(ctx, op); err != nil {
		return op, err
	}
	delete(m.coordinator.pending, id)
	op.Phase = port.PhaseReconciled
	return op, nil
}

// InitializeStorage must run with the instance lock held, before serving.
func (m *Manager) InitializeStorage(ctx context.Context, root string) error {
	if err := m.applyOfficialOrigin(ctx); err != nil {
		return err
	}
	if err := m.loadLicensing(ctx); err != nil {
		return err
	}
	used, err := m.repo.hasOrderFingerprints(ctx)
	if err != nil {
		return err
	}
	if _, err = pluginstorage.EnsureKey(root, !used); err != nil {
		return err
	}
	return m.Reconcile(ctx)
}

// ValidateSplitMode prevents a process without the plugin runtime from silently
// bypassing durable restrictions. Unrestricted existing split deployments work.
func (m *Manager) ValidateSplitMode(ctx context.Context) error {
	required, err := m.repo.hasRequirements(ctx)
	if err != nil {
		return err
	}
	if required {
		return fmt.Errorf("active plugin requirements require single-process all mode")
	}
	return nil
}

func (m *Manager) prunePackages(ctx context.Context, incoming string) error {
	store, ok := m.packages.(*FilePackages)
	if !ok {
		return nil
	}
	c := m.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.draining > 0 {
		return nil
	}
	for _, slot := range c.slots {
		if slot.refs > 0 {
			return nil
		}
	}
	protected := map[string]bool{incoming: true}
	ids, err := m.repo.installedIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		s, e := m.repo.status(ctx, id)
		if e != nil {
			return e
		}
		protected[s.DesiredDigest] = true
		protected[s.ObservedDigest] = true
	}
	return store.Prune(ctx, protected)
}

// ValidateServing refuses active rules without a purchase backend. The P2
// provider wires the real loader and every new-order entry enforces requirements.
func (m *Manager) ValidateServing(ctx context.Context) error {
	if m.loader != nil {
		return nil
	}
	required, err := m.repo.hasRequirements(ctx)
	if err != nil {
		return err
	}
	if required {
		return fmt.Errorf("active plugin rules require the P2 purchase backend; restore a compatible host")
	}
	return nil
}
