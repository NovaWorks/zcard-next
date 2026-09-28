package marketcontract

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSignedCatalogBoundary(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	c := Catalog{APIVersion: "1", Origin: "https://market.example.com", Revision: "2", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(), Entries: []Entry{}}
	e, _ := Sign(c, "first", key)
	keys := map[string]ed25519.PublicKey{"first": pub}
	if err := Verify(e, c.Origin, keys, now); err != nil {
		t.Fatal(err)
	}
	for _, modify := range []func(*Catalog){func(v *Catalog) { v.Revision = "02" }, func(v *Catalog) { v.ExpiresAt = now.Unix() - 1 }, func(v *Catalog) { v.ExpiresAt = v.IssuedAt + 301 }, func(v *Catalog) { v.Origin = "https://other.example.com" }, func(v *Catalog) { v.Entries = nil }} {
		bad := c
		modify(&bad)
		v, _ := Sign(bad, "first", key)
		if Verify(v, c.Origin, keys, now) == nil {
			t.Fatal("invalid signed catalog accepted")
		}
	}
	raw, _ := json.Marshal(e)
	if _, err := Decode(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(string(raw), `"keyId":"first"`, `"keyId":"first","keyId":"first"`, 1), string(raw) + `{}`, strings.Replace(string(raw), `"keyId":"first"`, `"unexpected":1,"keyId":"first"`, 1)} {
		if _, err := Decode([]byte(bad)); err == nil {
			t.Fatal("ambiguous JSON accepted")
		}
	}
	_, next, _ := ed25519.GenerateKey(rand.Reader)
	rotated, _ := Sign(c, "next", next)
	if Verify(rotated, c.Origin, keys, now) == nil {
		t.Fatal("unknown rotation accepted")
	}
	keys["next"] = next.Public().(ed25519.PublicKey)
	if err := Verify(rotated, c.Origin, keys, now); err != nil {
		t.Fatal(err)
	}
	delete(keys, "first")
	if Verify(e, c.Origin, keys, now) == nil {
		t.Fatal("revoked trust accepted")
	}
}
