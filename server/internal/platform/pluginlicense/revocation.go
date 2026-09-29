package pluginlicense

import (
	"crypto/ed25519"
	"regexp"
	"time"
)

// Revocations are cumulative safety decisions, independent of catalog
// withdrawal, unbinding and commercial refunds. Offline clients retain them.
type Revocation struct {
	PluginID string `json:"pluginId"`
	Version  string `json:"version"`
	Digest   string `json:"digest"`
	Reason   string `json:"reason"`
}
type RevocationList struct {
	SchemaVersion int          `json:"schemaVersion"`
	Issuer        string       `json:"issuer"`
	KeyID         string       `json:"keyId"`
	Sequence      string       `json:"sequence"`
	IssuedAt      int64        `json:"issuedAt"`
	Entries       []Revocation `json:"entries"`
}
type RevocationEnvelope struct {
	List      RevocationList `json:"list"`
	Signature []byte         `json:"signature"`
}

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validList(l RevocationList) error {
	if l.SchemaVersion != 1 || !ValidIssuer(l.Issuer) || !identifier.MatchString(l.KeyID) || l.IssuedAt <= 0 || l.Entries == nil || len(l.Entries) > 128 {
		return ErrInvalid
	}
	if _, err := Revision(l.Sequence); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, r := range l.Entries {
		k := r.PluginID + "@" + r.Version + "/" + r.Digest
		if !identifier.MatchString(r.PluginID) || len(r.Version) > 64 || (r.Version != "" && !validVersion(r.Version)) || (r.Digest != "" && !digestPattern.MatchString(r.Digest)) || len(r.Reason) == 0 || len(r.Reason) > 256 || seen[k] {
			return ErrInvalid
		}
		seen[k] = true
	}
	return nil
}
func SignRevocations(l RevocationList, key ed25519.PrivateKey) (RevocationEnvelope, error) {
	if err := validList(l); err != nil {
		return RevocationEnvelope{}, err
	}
	if len(key) != ed25519.PrivateKeySize {
		return RevocationEnvelope{}, ErrUntrusted
	}
	return RevocationEnvelope{List: l, Signature: ed25519.Sign(key, message("zcard.plugin.revocations.v1", l))}, nil
}
func AuthenticateRevocations(e RevocationEnvelope, roots map[string]Root, now time.Time) error {
	l := e.List
	if err := validList(l); err != nil {
		return err
	}
	r := roots[l.KeyID]
	if r.Issuer != l.Issuer || len(r.PublicKey) != ed25519.PublicKeySize || !ed25519.Verify(r.PublicKey, message("zcard.plugin.revocations.v1", l), e.Signature) {
		return ErrUntrusted
	}
	if l.IssuedAt > now.Add(30*time.Second).Unix() {
		return ErrInvalid
	}
	return nil
}
func (s State) AcceptRevocations(e RevocationEnvelope, roots map[string]Root, now time.Time) (State, error) {
	if err := AuthenticateRevocations(e, roots, now); err != nil {
		return s, err
	}
	if old, ok := s.Revocations[e.List.Issuer]; ok {
		if err := advance(e.List.Sequence, old.List.Sequence, hash(e.List), hash(old.List)); err != nil {
			return s, err
		}
		if e.List.IssuedAt < old.List.IssuedAt {
			return s, ErrReplay
		}
		// Safety bans cannot disappear because an issuer accidentally publishes
		// an incomplete snapshot; reinstatement needs a separately defined protocol.
		entries := map[Revocation]bool{}
		for _, r := range e.List.Entries {
			entries[r] = true
		}
		for _, r := range old.List.Entries {
			if !entries[r] {
				return s, ErrReplay
			}
		}
	} else if len(s.Revocations) >= MaxIssuers {
		return s, ErrInvalid
	}
	out := s.copy()
	e.Signature = append([]byte(nil), e.Signature...)
	e.List.Entries = append([]Revocation{}, e.List.Entries...)
	out.Revocations[e.List.Issuer] = e
	return out, nil
}
