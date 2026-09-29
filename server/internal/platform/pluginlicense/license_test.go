package pluginlicense

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (License, ed25519.PrivateKey, map[string]Root, time.Time) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1800000000, 0)
	l := License{SchemaVersion: 1, KeyID: "license-test", Issuer: "https://market.example.com", LicenseID: "license-a", PluginID: "plugin-a", InstanceID: "instance-0001", Revision: "1", IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: now.Unix() + 3600, Status: "active", MinVersion: "1.0.0", MaxVersionExclusive: "2.0.0"}
	return l, key, map[string]Root{l.KeyID: {Issuer: l.Issuer, PublicKey: pub}}, now
}
func signed(t *testing.T, l License, key ed25519.PrivateKey) Envelope {
	t.Helper()
	e, err := Sign(l, key)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestIndependentIdentityAndExactExpiry(t *testing.T) {
	l, key, roots, now := fixture(t)
	e := signed(t, l, key)
	if err := Authenticate(e, roots, l.InstanceID, "", now); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*License)
	}{
		{"plugin-wildcard", func(l *License) { l.PluginID = "*" }},
		{"namespace-wildcard", func(l *License) { l.PluginID = "plugin:*" }},
		{"schema", func(l *License) { l.SchemaVersion = 2 }},
		{"revision-zero", func(l *License) { l.Revision = "0" }},
		{"revision-leading-zero", func(l *License) { l.Revision = "01" }},
		{"interval", func(l *License) { l.ExpiresAt = l.NotBefore }},
		{"domain-path", func(l *License) { l.Domain = "shop.example/path" }},
		{"domain-port", func(l *License) { l.Domain = "shop.example:443" }},
		{"version", func(l *License) { l.MinVersion = "bad" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := l
			tc.change(&v)
			if _, err := Sign(v, key); err == nil {
				t.Fatal("invalid signed")
			}
		})
	}
	if err := Authenticate(e, roots, "instance-0002", "", now); err == nil {
		t.Fatal("wrong instance")
	}
	if err := Authenticate(e, nil, l.InstanceID, "", now); err == nil {
		t.Fatal("self trust")
	}
	wrong := map[string]Root{l.KeyID: {Issuer: "https://different.example", PublicKey: roots[l.KeyID].PublicKey}}
	if err := Authenticate(e, wrong, l.InstanceID, "", now); err == nil {
		t.Fatal("wrong issuer")
	}
	l.Domain = "shop.example.com"
	e = signed(t, l, key)
	for _, d := range []string{"", "other.example.com", "shop.example.com:443"} {
		if err := Authenticate(e, roots, l.InstanceID, d, now); err == nil {
			t.Fatal("wrong or missing trusted domain", d)
		}
	}
	if err := Authenticate(e, roots, l.InstanceID, "SHOP.EXAMPLE.COM.", now); err != nil {
		t.Fatal(err)
	}
	if l.Check("plugin-b", "1.0.0", now) != Incompatible || l.Check(l.PluginID, "2.0.0", now) != Incompatible {
		t.Fatal("identity/version not scoped")
	}
	if l.Check(l.PluginID, "1.0.0", time.Unix(l.ExpiresAt-1, 999999999)) != Valid || l.Check(l.PluginID, "1.0.0", time.Unix(l.ExpiresAt, 0)) != Expired {
		t.Fatal("expiry boundary")
	}
	if l.Check(l.PluginID, "1.0.0", now.Add(-time.Second)) != NotYetValid {
		t.Fatal("not before")
	}
}
func TestRevisionPersistenceAndNoReplacementIdentity(t *testing.T) {
	l, key, roots, now := fixture(t)
	s, err := NewState().Accept(signed(t, l, key), roots, l.InstanceID, "", now)
	if err != nil {
		t.Fatal(err)
	}
	l.Revision = "2"
	l.Status = "revoked"
	s, err = s.Accept(signed(t, l, key), roots, l.InstanceID, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Accept(signed(t, l, key), roots, l.InstanceID, "", now); err != nil {
		t.Fatal("idempotence", err)
	}
	for _, change := range []func(*License){func(l *License) { l.Revision = "1" }, func(l *License) { l.Status = "active" }, func(l *License) { l.LicenseID = "another-license"; l.Revision = "3" }} {
		v := l
		change(&v)
		if _, err := s.Accept(signed(t, v, key), roots, l.InstanceID, "", now); !errors.Is(err, ErrReplay) {
			t.Fatal("replay", err)
		}
	}
	raw, _ := json.Marshal(s)
	var restored State
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Check(l.Issuer, l.InstanceID, l.PluginID, "1.0.0", "", "paid", now) != Revoked {
		t.Fatal("revocation lost")
	}
	if restored.Check("https://different.example", l.InstanceID, l.PluginID, "1.0.0", "", "paid", now) != Missing {
		t.Fatal("issuer substitution")
	}
	if restored.Check(l.Issuer, l.InstanceID, l.PluginID, "1.0.0", "", "free", now.Add(24*time.Hour)) != Valid {
		t.Fatal("commercial revocation stopped old free artifact")
	}
	b := l
	b.PluginID = "plugin-b"
	b.LicenseID = "license-b"
	b.Revision = "1"
	b.Status = "active"
	restored, err = restored.Accept(signed(t, b, key), roots, l.InstanceID, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Check(l.Issuer, l.InstanceID, b.PluginID, "1.0.0", "", "paid", now) != Valid || restored.Check(l.Issuer, l.InstanceID, l.PluginID, "1.0.0", "", "paid", now) != Revoked {
		t.Fatal("entitlements overwrite each other")
	}
}
func TestSafetyListsAreCumulativeAndIndependent(t *testing.T) {
	l, key, roots, now := fixture(t)
	list := RevocationList{SchemaVersion: 1, KeyID: l.KeyID, Issuer: l.Issuer, Sequence: "1", IssuedAt: now.Unix(), Entries: []Revocation{{PluginID: l.PluginID, Version: "1.0.0", Reason: "vulnerable hook"}}}
	e, err := SignRevocations(list, key)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewState().AcceptRevocations(e, roots, now)
	if err != nil {
		t.Fatal(err)
	}
	if s.Check(l.Issuer, l.InstanceID, l.PluginID, "1.0.0", "", "free", now) != SafetyRevoked {
		t.Fatal("free unsafe artifact bypass")
	}
	if s.Check(l.Issuer, l.InstanceID, l.PluginID, "1.0.1", "", "free", now) != Valid {
		t.Fatal("other version blocked")
	}
	list.Sequence = "2"
	list.Entries = []Revocation{}
	e, _ = SignRevocations(list, key)
	if _, err := s.AcceptRevocations(e, roots, now); !errors.Is(err, ErrReplay) {
		t.Fatal("safety removal allowed", err)
	}
	list.Sequence = "1"
	list.Entries = []Revocation{{PluginID: l.PluginID, Version: "1.0.0", Reason: "changed reason"}}
	e, _ = SignRevocations(list, key)
	if _, err := s.AcceptRevocations(e, roots, now); !errors.Is(err, ErrReplay) {
		t.Fatal("same sequence conflict", err)
	}
	// Signing domains cannot be interchanged even with the same public key.
	license := signed(t, l, key)
	license.Signature = e.Signature
	if err := Authenticate(license, roots, l.InstanceID, "", now); !errors.Is(err, ErrUntrusted) {
		t.Fatal("signature domain confusion", err)
	}
}
func TestStrictDocuments(t *testing.T) {
	l, key, _, _ := fixture(t)
	raw, _ := json.Marshal(signed(t, l, key))
	var e Envelope
	if err := Decode(raw, &e); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`null`, strings.Replace(string(raw), `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1), strings.Replace(string(raw), `"schemaVersion":1`, `"schemaVersion":1,"unknown":true`, 1), string(raw) + `{}`, strings.Repeat("[", 20) + strings.Repeat("]", 20), strings.Repeat("x", MaxDocumentBytes+1)} {
		if err := Decode([]byte(bad), &e); err == nil {
			t.Fatal("ambiguous JSON accepted")
		}
	}
}

func TestLicenseV1SigningPreimage(t *testing.T) {
	l, _, _, _ := fixture(t)
	want := "zcard.plugin.license.v1\n" + `{"schemaVersion":1,"keyId":"license-test","issuer":"https://market.example.com","licenseId":"license-a","pluginId":"plugin-a","instanceId":"instance-0001","domain":"","revision":"1","issuedAt":1800000000,"notBefore":1800000000,"expiresAt":1800003600,"status":"active","minVersion":"1.0.0","maxVersionExclusive":"2.0.0"}`
	if string(message("zcard.plugin.license.v1", l)) != want {
		t.Fatal("v1 signing preimage changed; introduce a new schema/domain")
	}
}
