// Package plugincontract defines the versioned wire protocol for trusted plugins.
// It has no database, business service, JavaScript runtime or network dependency.
package plugincontract

import "encoding/json"

const (
	SchemaVersion      = 1
	HookOrderPreCreate = "order.pre_create.v1"
	ProductEditor      = "admin.product.edit.extra.v1"
	MaxJSONBytes       = 64 << 10
	MaxArchiveBytes    = 8 << 20
	MaxExpandedBytes   = 16 << 20
	MaxScriptBytes     = 512 << 10
	MaxArchiveFiles    = 32
)

type Kind string

const (
	ManifestKind Kind = "manifest"
	ConfigKind   Kind = "config"
	InputKind    Kind = "input"
	DecisionKind Kind = "decision"
	ArtifactKind Kind = "artifact"
)

// Decimal is an exact, canonical base-10 uint64 on the wire. JSON numbers are
// forbidden for IDs, generations and revisions. Validate before using a DTO.
type Decimal string

type Manifest struct {
	SchemaVersion        int              `json:"schemaVersion"`
	ID                   string           `json:"id"`
	Version              string           `json:"version"`
	Runtime              string           `json:"runtime"`
	Entry                string           `json:"entry"`
	HostAPIVersion       string           `json:"hostApiVersion"`
	Core                 CoreRange        `json:"core"`
	RequiredCapabilities []string         `json:"requiredCapabilities"`
	Scopes               []string         `json:"scopes"`
	ConfigSchemaVersion  int              `json:"configSchemaVersion"`
	UIContributions      []UIContribution `json:"uiContributions"`
	Entitlement          Entitlement      `json:"entitlement"`
}

type CoreRange struct {
	MinInclusive string `json:"minInclusive"`
	MaxExclusive string `json:"maxExclusive"`
}
type Entitlement struct {
	Mode string `json:"mode"`
}
type UIContribution struct {
	ExtensionPoint string    `json:"extensionPoint"`
	Required       bool      `json:"required"`
	Fields         []UIField `json:"fields"`
}
type UIField struct {
	Key           string `json:"key"`
	Type          string `json:"type"`
	Label         string `json:"label"`
	OptionsSource string `json:"optionsSource"`
}
type Config struct {
	SchemaVersion   int       `json:"schemaVersion"`
	Revision        Decimal   `json:"revision"`
	Enabled         bool      `json:"enabled"`
	AllowedLevelIDs []Decimal `json:"allowedLevelIds"`
}
type Member struct {
	Authenticated    bool    `json:"authenticated"`
	EffectiveLevelID Decimal `json:"effectiveLevelId"`
}
type Input struct {
	SchemaVersion int     `json:"schemaVersion"`
	Hook          string  `json:"hook"`
	PluginID      string  `json:"pluginId"`
	Generation    Decimal `json:"generation"`
	SubsiteID     Decimal `json:"subsiteId"`
	ProductID     Decimal `json:"productId"`
	SKUID         Decimal `json:"skuId"`
	Quantity      int     `json:"quantity"`
	Channel       string  `json:"channel"`
	Member        Member  `json:"member"`
	Config        Config  `json:"config"`
}
type Reason string

const (
	OK                Reason = "OK"
	LoginRequired     Reason = "LOGIN_REQUIRED"
	MemberLevelDenied Reason = "MEMBER_LEVEL_DENIED"
	SupplyRestricted  Reason = "SUPPLY_RESTRICTED"
)

type Decision struct {
	Allow  bool   `json:"allow"`
	Reason Reason `json:"reason"`
}

// ArtifactDescriptor is detached from the ZIP it hashes. Field order below is
// part of signing protocol v1; signing is over these fields, never a Go map.
type ArtifactDescriptor struct {
	SchemaVersion  int     `json:"schemaVersion"`
	PluginID       string  `json:"pluginId"`
	Version        string  `json:"version"`
	ManifestSHA256 string  `json:"manifestSHA256"`
	ArchiveSHA256  string  `json:"archiveSHA256"`
	ArchiveBytes   Decimal `json:"archiveBytes"`
	KeyID          string  `json:"keyId"`
}

// SigningMessage returns the deterministic Ed25519 preimage, not a signature
// or proof of trust. P1 must verify it using a locally approved key.
func SigningMessage(d ArtifactDescriptor) ([]byte, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if err = Validate(ArtifactKind, b); err != nil {
		return nil, err
	}
	return append([]byte("zcard.plugin.artifact.v1\n"), b...), nil
}

type ErrorCode string

const (
	InvalidContract ErrorCode = "PLUGIN_INVALID_CONTRACT"
	Incompatible    ErrorCode = "PLUGIN_INCOMPATIBLE"
	Unavailable     ErrorCode = "PLUGIN_UNAVAILABLE"
	Conflict        ErrorCode = "PLUGIN_CONFLICT"
	Forbidden       ErrorCode = "PLUGIN_FORBIDDEN"
	PaidUnsupported ErrorCode = "PLUGIN_PAID_UNSUPPORTED"
)

// Error's Detail is for operators; adapters must map Code to a fixed public
// message, rather than returning schema internals or private level IDs.
type Error struct {
	Code   ErrorCode
	Detail string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Detail }
