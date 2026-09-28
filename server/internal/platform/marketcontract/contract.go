// Package marketcontract is the public, signed free-plugin distribution protocol.
// It contains no marketplace persistence or operator implementation.
package marketcontract

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"time"

	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
)

const APIVersion = "1"
const Prefix = "/api/market/v1"
const MaxEntries = 128
const MaxCatalogBytes = 2 << 20
const CatalogLifetime = 5 * time.Minute

type Capabilities struct {
	APIVersion       string `json:"apiVersion"`
	FreeDistribution bool   `json:"freeDistribution"`
	MaxEntries       int    `json:"maxEntries"`
	Pagination       string `json:"pagination"`
}
type Entry struct {
	Name         string                `json:"name"`
	Descriptor   pc.ArtifactDescriptor `json:"descriptor"`
	Signature    []byte                `json:"signature"`
	Manifest     pc.Manifest           `json:"manifest"`
	ArtifactPath string                `json:"artifactPath"`
}
type Catalog struct {
	APIVersion string     `json:"apiVersion"`
	Origin     string     `json:"origin"`
	Revision   pc.Decimal `json:"revision"`
	IssuedAt   int64      `json:"issuedAt"`
	ExpiresAt  int64      `json:"expiresAt"`
	Entries    []Entry    `json:"entries"`
}
type Envelope struct {
	Catalog   Catalog `json:"catalog"`
	KeyID     string  `json:"keyId"`
	Signature []byte  `json:"signature"`
}
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

func Origin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("market origin must be an absolute HTTP(S) origin without credentials, path or query")
	}
	return u.Scheme + "://" + u.Host, nil
}
func Revision(s pc.Decimal) (uint64, error) {
	n, err := strconv.ParseUint(string(s), 10, 64)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != string(s) {
		return 0, fmt.Errorf("invalid catalog revision")
	}
	return n, nil
}
func SigningMessage(c Catalog) ([]byte, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return append([]byte("zcard.market.catalog.v1\n"), raw...), nil
}
func Sign(c Catalog, keyID string, key ed25519.PrivateKey) (Envelope, error) {
	if len(key) != ed25519.PrivateKeySize {
		return Envelope{}, fmt.Errorf("invalid catalog signing key")
	}
	raw, err := SigningMessage(c)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{Catalog: c, KeyID: keyID, Signature: ed25519.Sign(key, raw)}, nil
}

// EntriesHash excludes freshness timestamps, allowing signed renewal of the
// same immutable revision while detecting equivocation at that revision.
func EntriesHash(c Catalog) string {
	raw, _ := json.Marshal(c.Entries)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func Decode(raw []byte) (Envelope, error) {
	var e Envelope
	if len(raw) > MaxCatalogBytes {
		return e, fmt.Errorf("catalog size limit")
	}
	if err := uniqueJSON(raw); err != nil {
		return e, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&e); err != nil {
		return e, err
	}
	if d.Decode(new(any)) != io.EOF {
		return e, fmt.Errorf("trailing catalog data")
	}
	return e, nil
}
func Verify(e Envelope, origin string, keys map[string]ed25519.PublicKey, now time.Time) error {
	c := e.Catalog
	key := keys[e.KeyID]
	raw, err := SigningMessage(c)
	if err != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, raw, e.Signature) {
		return fmt.Errorf("untrusted catalog signature")
	}
	normalized, err := Origin(c.Origin)
	if err != nil || normalized != origin || c.Origin != origin || c.APIVersion != APIVersion {
		return fmt.Errorf("catalog origin or API mismatch")
	}
	if _, err = Revision(c.Revision); err != nil {
		return err
	}
	if c.IssuedAt <= 0 || c.IssuedAt > now.Add(30*time.Second).Unix() || c.ExpiresAt <= now.Unix() || c.ExpiresAt <= c.IssuedAt || c.ExpiresAt-c.IssuedAt > int64(CatalogLifetime/time.Second) {
		return fmt.Errorf("catalog expired or invalid validity interval")
	}
	if len(c.Entries) > MaxEntries || c.Entries == nil {
		return fmt.Errorf("catalog entry limit")
	}
	seen := map[string]bool{}
	for _, v := range c.Entries {
		id := v.Descriptor.PluginID + "@" + v.Descriptor.Version
		if seen[id] || len(v.Name) == 0 || len(v.Name) > 128 {
			return fmt.Errorf("duplicate or invalid catalog entry")
		}
		seen[id] = true
		msg, err := pc.SigningMessage(v.Descriptor)
		key := keys[v.Descriptor.KeyID]
		if err != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, msg, v.Signature) {
			return fmt.Errorf("untrusted artifact descriptor")
		}
		manifest, _ := json.Marshal(v.Manifest)
		if pc.Validate(pc.ManifestKind, manifest) != nil || v.Manifest.ID != v.Descriptor.PluginID || v.Manifest.Version != v.Descriptor.Version || v.Manifest.Entitlement.Mode != "free" {
			return fmt.Errorf("invalid or unsupported entry manifest")
		}
		// The ZIP verifier checks the exact manifest bytes against the descriptor.
		if v.ArtifactPath != Prefix+"/artifacts/"+v.Descriptor.ArchiveSHA256 {
			return fmt.Errorf("artifact path mismatch")
		}
	}
	return nil
}
