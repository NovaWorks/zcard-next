// Package port exposes plugin coordination contracts without Ent or handlers.
// P0 defines these interfaces; P1/P2 provide and wire their implementations.
package port

import (
	"context"
	"io"

	"github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
)

type RuleKey struct {
	SubsiteID, ProductID uint64
	PluginID             string
}
type Expected struct {
	Generation, ConfigRevision, RequirementRevision uint64
	SchemaVersion                                   int
}

// Product access and the explicit scope must be checked by the host service.
// Zero SubsiteID is valid only for an explicitly authorized main-site request.
type Actor struct {
	AdminID       uint64 `json:"admin_id,string"`
	SubsiteID     uint64 `json:"subsite_id,string"`
	ScopeVerified bool   `json:"scope_verified"`
	InstanceAdmin bool   `json:"instance_admin"`
	LocalOperator bool   `json:"local_operator"`
}
type Requirement struct {
	Key      RuleKey
	Required bool
	Revision uint64
}
type Rule struct {
	Requirement Requirement
	Config      plugincontract.Config
}
type SaveConfig struct {
	Key      RuleKey
	Actor    Actor
	Expected Expected
	Config   plugincontract.Config
}
type ReleaseRule struct {
	Key                         RuleKey
	Actor                       Actor
	ExpectedRequirementRevision uint64
}

type ConfigStore interface {
	Get(context.Context, RuleKey) (Rule, error)
	// Save atomically CAS-writes config, requirement, normalized level references
	// and the product rule anchor under product/level locks. Caller cannot write
	// a requirement separately or commit a half-configured active rule.
	Save(context.Context, SaveConfig) (Rule, error)
	// Release requires no installed package, usable config JSON or runtime.
	// It CAS-clears the requirement, removes level references and invalidates
	// config revision in one transaction; stale pages cannot resurrect the rule.
	Release(context.Context, ReleaseRule) (Requirement, error)
}
type RequirementStore interface {
	// Read in the caller's purchase transaction, including absent requirements.
	ListForProducts(context.Context, uint64, []uint64) ([]Requirement, error)
	// Called after locking the global level during deletion, across every subsite.
	// Query errors deny deletion.
	LevelReferenced(context.Context, uint64) (bool, error)
}

type Artifact struct {
	Descriptor plugincontract.ArtifactDescriptor
	Manifest   plugincontract.Manifest
}
type PackageStore interface {
	// Stage must verify the detached descriptor signature, archive and manifest
	// before atomic publication; reads are bounded and reject unsafe ZIP entries.
	Stage(ctx context.Context, descriptor []byte, signature []byte, archive io.Reader) (Artifact, error)
	Open(ctx context.Context, archiveSHA256 string) (Artifact, io.ReadCloser, error)
}

type Phase string

const (
	PhaseStaged     Phase = "staged"
	PhasePreparing  Phase = "preparing"
	PhaseReady      Phase = "ready"
	PhasePublished  Phase = "published"
	PhaseReconciled Phase = "reconciled"
	PhaseFailed     Phase = "failed"
)

type BlockReason string

const (
	BlockMissing      BlockReason = "package_missing"
	BlockIncompatible BlockReason = "incompatible"
	BlockRuntime      BlockReason = "runtime_fault"
	BlockExpired      BlockReason = "entitlement_expired"
	BlockRevoked      BlockReason = "entitlement_revoked"
)

type State struct {
	PluginID              string        `json:"plugin_id"`
	DesiredEnabled        bool          `json:"desired_enabled"`
	DesiredGeneration     uint64        `json:"desired_generation,string"`
	ObservedGeneration    uint64        `json:"observed_generation,string"`
	OperationID           string        `json:"operation_id"`
	Phase                 Phase         `json:"phase"`
	BlockReasons          []BlockReason `json:"block_reasons"`
	ReconciliationPending bool          `json:"reconciliation_pending"`
}

// PreparedRuntime is inert until the lifecycle coordinator publishes it.
// Close is called only after the last lease drains; Prepare cannot mutate rules.
type PreparedRuntime interface {
	Evaluate(context.Context, plugincontract.Input) (plugincontract.Decision, error)
	Close() error
}
type RuntimeLoader interface {
	Prepare(context.Context, Artifact) (PreparedRuntime, error)
}
type RuntimeLease interface {
	Generation() uint64
	Evaluate(context.Context, plugincontract.Input) (plugincontract.Decision, error)
	Release()
}
type RegistrySnapshot interface {
	// Acquire fails closed for a required but missing/disabled/blocked plugin.
	// A lease pins one generation until all items of an order finish evaluation.
	Acquire(context.Context, string) (RuntimeLease, error)
}
type GateItem struct {
	Requirement Requirement
	Input       plugincontract.Input
}
type GateResult struct {
	ProductID, Generation, ConfigRevision, RequirementRevision uint64
	Decision                                                   plugincontract.Decision
}

// Deprecated: P0 draft. Purchases use PurchaseGate/PurchaseSession so runtime
// leases are acquired before any SQL product locks.
type GateEvaluator interface {
	// Inputs come only from trusted host projections. EvaluateAll is read-only;
	// any denial/error prevents all purchase side effects. P2 must consult stored
	// requirements even when the active registry is empty.
	EvaluateAll(context.Context, []GateItem) ([]GateResult, error)
}
