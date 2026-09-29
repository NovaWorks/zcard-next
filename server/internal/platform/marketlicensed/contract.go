// Package marketlicensed adds a versioned catalog for paid artifacts without
// changing the frozen v1 free-distribution signature or acceptance rules.
package marketlicensed

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	mc "github.com/NovaWorks/zcard-next/server/internal/platform/marketcontract"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"time"
)

const APIVersion = "2"
const Prefix = "/api/market/v2"

type Envelope = mc.Envelope

func SigningMessage(c mc.Catalog) ([]byte, error) {
	raw, e := json.Marshal(c)
	return append([]byte("zcard.market.catalog.v2\n"), raw...), e
}
func Sign(c mc.Catalog, id string, key ed25519.PrivateKey) (Envelope, error) {
	if len(key) != ed25519.PrivateKeySize {
		return Envelope{}, fmt.Errorf("invalid catalog key")
	}
	raw, e := SigningMessage(c)
	if e != nil {
		return Envelope{}, e
	}
	return Envelope{Catalog: c, KeyID: id, Signature: ed25519.Sign(key, raw)}, nil
}
func Verify(e Envelope, origin string, keys map[string]ed25519.PublicKey, now time.Time) error {
	c := e.Catalog
	key := keys[e.KeyID]
	raw, err := SigningMessage(c)
	if err != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, raw, e.Signature) {
		return fmt.Errorf("untrusted catalog signature")
	}
	normalized, err := mc.Origin(c.Origin)
	if err != nil || normalized != origin || c.Origin != origin || c.APIVersion != APIVersion {
		return fmt.Errorf("catalog origin or API mismatch")
	}
	if _, err = mc.Revision(c.Revision); err != nil {
		return err
	}
	if c.IssuedAt <= 0 || c.IssuedAt > now.Add(30*time.Second).Unix() || c.ExpiresAt <= now.Unix() || c.ExpiresAt <= c.IssuedAt || c.ExpiresAt-c.IssuedAt > int64(mc.CatalogLifetime/time.Second) {
		return fmt.Errorf("catalog expired or invalid validity interval")
	}
	if len(c.Entries) > mc.MaxEntries || c.Entries == nil {
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
		if pc.Validate(pc.ManifestKind, manifest) != nil || v.Manifest.ID != v.Descriptor.PluginID || v.Manifest.Version != v.Descriptor.Version || (v.Manifest.Entitlement.Mode != "free" && v.Manifest.Entitlement.Mode != "paid") {
			return fmt.Errorf("invalid or unsupported entry manifest")
		}
		// The ZIP verifier checks the exact manifest bytes against the descriptor.
		if v.ArtifactPath != Prefix+"/artifacts/"+v.Descriptor.ArchiveSHA256 {
			return fmt.Errorf("artifact path mismatch")
		}
	}
	return nil
}
