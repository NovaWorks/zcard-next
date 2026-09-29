package marketlicensed

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	mc "github.com/NovaWorks/zcard-next/server/internal/platform/marketcontract"
	"strings"
	"testing"
	"time"
)

func TestLicensedCatalogSignatureBoundary(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	c := mc.Catalog{APIVersion: "2", Origin: "https://market.example.com", Revision: "2", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(), Entries: []mc.Entry{}}
	e, _ := Sign(c, "first", key)
	keys := map[string]ed25519.PublicKey{"first": pub}
	if err := Verify(e, c.Origin, keys, now); err != nil {
		t.Fatal(err)
	}
	for _, modify := range []func(*mc.Catalog){func(v *mc.Catalog) { v.Revision = "02" }, func(v *mc.Catalog) { v.ExpiresAt = now.Unix() - 1 }, func(v *mc.Catalog) { v.ExpiresAt = v.IssuedAt + 301 }, func(v *mc.Catalog) { v.Origin = "https://other.example.com" }, func(v *mc.Catalog) { v.Entries = nil }} {
		bad := c
		modify(&bad)
		v, _ := Sign(bad, "first", key)
		if Verify(v, c.Origin, keys, now) == nil {
			t.Fatal("invalid signed catalog accepted")
		}
	}
	if mc.Verify(e, c.Origin, keys, now) == nil {
		t.Fatal("licensed catalog accepted under frozen v1")
	}
	raw, _ := json.Marshal(e)
	if _, err := mc.Decode(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(string(raw), `"keyId":"first"`, `"keyId":"first","keyId":"first"`, 1), string(raw) + `{}`, strings.Replace(string(raw), `"keyId":"first"`, `"unexpected":1,"keyId":"first"`, 1)} {
		if _, err := mc.Decode([]byte(bad)); err == nil {
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
