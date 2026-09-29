// Package pluginlicense implements independent, offline plugin entitlements.
// It never reads core subscription files or learns trust roots from responses.
package pluginlicense

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const MaxDocumentBytes = 64 << 10
const MaxLicenses = 256
const MaxIssuers = 32

var (
	ErrInvalid   = errors.New("plugin license: invalid document")
	ErrUntrusted = errors.New("plugin license: untrusted signature or issuer")
	ErrReplay    = errors.New("plugin license: stale or conflicting revision")
	identifier   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	instance     = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
)

// Root pins both the key and its issuing authority. Reusing a key at another
// endpoint does not let that endpoint impersonate a different issuer.
// PerpetualExpiry is the v1 wire sentinel for a purchased perpetual license.
const PerpetualExpiry int64 = 253402300799

type Root struct {
	Issuer    string `json:"issuer"`
	PublicKey []byte `json:"publicKey"`
}

type License struct {
	SchemaVersion       int    `json:"schemaVersion"`
	KeyID               string `json:"keyId"`
	Issuer              string `json:"issuer"`
	LicenseID           string `json:"licenseId"`
	PluginID            string `json:"pluginId"`
	InstanceID          string `json:"instanceId"`
	Domain              string `json:"domain"`
	Revision            string `json:"revision"`
	IssuedAt            int64  `json:"issuedAt"`
	NotBefore           int64  `json:"notBefore"`
	ExpiresAt           int64  `json:"expiresAt"`
	Status              string `json:"status"`
	MinVersion          string `json:"minVersion"`
	MaxVersionExclusive string `json:"maxVersionExclusive"`
}
type Envelope struct {
	License   License `json:"license"`
	Signature []byte  `json:"signature"`
}

type Reason string

const (
	Valid         Reason = ""
	Missing       Reason = "entitlement_missing"
	Expired       Reason = "entitlement_expired"
	Revoked       Reason = "entitlement_revoked"
	NotYetValid   Reason = "entitlement_not_yet_valid"
	Incompatible  Reason = "entitlement_incompatible"
	SafetyRevoked Reason = "security_revoked"
)

func Revision(raw string) (uint64, error) {
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != raw {
		return 0, ErrInvalid
	}
	return n, nil
}
func ValidIssuer(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == "" && u.Opaque == "" && u.Host == strings.ToLower(u.Host) && u.String() == raw
}

// Domain uses ASCII DNS names (IDNs must be configured as A-labels), never a
// request Host header. Empty means that the license has no domain restriction.
func Domain(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSuffix(raw, "."))
	if value == "" {
		return "", nil
	}
	if len(value) > 253 {
		return "", ErrInvalid
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInvalid
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", ErrInvalid
			}
		}
	}
	return value, nil
}
func validate(l License) error {
	domain, err := Domain(l.Domain)
	if err != nil || domain != l.Domain || l.SchemaVersion != 1 || !identifier.MatchString(l.KeyID) || !identifier.MatchString(l.LicenseID) || !identifier.MatchString(l.PluginID) || !instance.MatchString(l.InstanceID) || !ValidIssuer(l.Issuer) {
		return ErrInvalid
	}
	if _, err := Revision(l.Revision); err != nil {
		return err
	}
	if l.IssuedAt <= 0 || l.NotBefore < l.IssuedAt || l.ExpiresAt <= l.NotBefore || l.ExpiresAt > PerpetualExpiry || (l.Status != "active" && l.Status != "revoked") {
		return ErrInvalid
	}
	if !semver.IsValid("v"+l.MinVersion) || !semver.IsValid("v"+l.MaxVersionExclusive) || semver.Compare("v"+l.MinVersion, "v"+l.MaxVersionExclusive) >= 0 {
		return ErrInvalid
	}
	return nil
}
func message(prefix string, v any) []byte {
	raw, _ := json.Marshal(v)
	return append([]byte(prefix+"\n"), raw...)
}
func Sign(l License, key ed25519.PrivateKey) (Envelope, error) {
	if err := validate(l); err != nil {
		return Envelope{}, err
	}
	if len(key) != ed25519.PrivateKeySize {
		return Envelope{}, ErrUntrusted
	}
	return Envelope{License: l, Signature: ed25519.Sign(key, message("zcard.plugin.license.v1", l))}, nil
}

// Authenticate verifies identity and signature, but deliberately accepts an
// expired or revoked document so newer revocations can advance durable state.
func Authenticate(e Envelope, roots map[string]Root, instanceID, domain string, now time.Time) error {
	l := e.License
	if err := validate(l); err != nil {
		return err
	}
	r := roots[l.KeyID]
	if r.Issuer != l.Issuer || len(r.PublicKey) != ed25519.PublicKeySize || !ed25519.Verify(r.PublicKey, message("zcard.plugin.license.v1", l), e.Signature) {
		return ErrUntrusted
	}
	normalized, err := Domain(domain)
	if err != nil || l.InstanceID != instanceID || (l.Domain != "" && (normalized == "" || normalized != l.Domain)) || l.IssuedAt > now.Add(30*time.Second).Unix() {
		return ErrInvalid
	}
	return nil
}
func (l License) Check(pluginID, version string, now time.Time) Reason {
	if l.PluginID != pluginID || !semver.IsValid("v"+version) || semver.Compare("v"+version, "v"+l.MinVersion) < 0 || semver.Compare("v"+version, "v"+l.MaxVersionExclusive) >= 0 {
		return Incompatible
	}
	if l.Status == "revoked" {
		return Revoked
	}
	if now.Unix() < l.NotBefore {
		return NotYetValid
	}
	if now.Unix() >= l.ExpiresAt {
		return Expired
	}
	return Valid
}
func hash(v any) string {
	raw, _ := json.Marshal(v)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func slot(l License) string { return l.Issuer + "/" + l.InstanceID + "/" + l.PluginID }

// State is signed security state, not an active-runtime flag. Persist the
// returned next state atomically BEFORE publishing it to concurrent requests.
// There is intentionally no reset/remove API for rollback or endpoint changes.
type State struct {
	Licenses    map[string]Envelope           `json:"licenses"`
	Revocations map[string]RevocationEnvelope `json:"revocations"`
}

func NewState() State {
	return State{Licenses: map[string]Envelope{}, Revocations: map[string]RevocationEnvelope{}}
}
func (s State) copy() State {
	out := NewState()
	for k, v := range s.Licenses {
		out.Licenses[k] = v
	}
	for k, v := range s.Revocations {
		out.Revocations[k] = v
	}
	return out
}
func advance(revision, oldRevision, digest, oldDigest string) error {
	n, err := Revision(revision)
	if err != nil {
		return err
	}
	p, err := Revision(oldRevision)
	if err != nil {
		return err
	}
	if n < p || n == p && digest != oldDigest {
		return ErrReplay
	}
	return nil
}
func (s State) Accept(e Envelope, roots map[string]Root, instanceID, domain string, now time.Time) (State, error) {
	if err := Authenticate(e, roots, instanceID, domain, now); err != nil {
		return s, err
	}
	k := slot(e.License)
	if old, ok := s.Licenses[k]; ok {
		// License IDs cannot be replaced to reset the entitlement's revision.
		if old.License.LicenseID != e.License.LicenseID {
			return s, ErrReplay
		}
		if err := advance(e.License.Revision, old.License.Revision, hash(e.License), hash(old.License)); err != nil {
			return s, err
		}
	} else if len(s.Licenses) >= MaxLicenses {
		return s, fmt.Errorf("plugin license: capacity reached")
	}
	out := s.copy()
	e.Signature = append([]byte(nil), e.Signature...)
	out.Licenses[k] = e
	return out, nil
}

// Check uses one issuer chosen by deployment configuration, not whichever
// document happens to grant permission. A second issuer cannot mask revocation.
func (s State) Check(issuer, instanceID, pluginID, version, digest, mode string, now time.Time) Reason {
	for _, e := range s.Revocations {
		for _, r := range e.List.Entries {
			if r.PluginID == pluginID && (r.Version == "" || r.Version == version) && (r.Digest == "" || r.Digest == digest) {
				return SafetyRevoked
			}
		}
	}
	if mode == "free" {
		return Valid
	}
	if mode != "paid" {
		return Incompatible
	}
	e, ok := s.Licenses[issuer+"/"+instanceID+"/"+pluginID]
	if !ok {
		return Missing
	}
	return e.License.Check(pluginID, version, now)
}
