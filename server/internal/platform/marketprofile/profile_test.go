package marketprofile

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	ma "github.com/NovaWorks/zcard-next/server/internal/platform/marketaccount"
	"os"
	"path/filepath"
	"testing"
)

func TestProfileReleaseGate(t *testing.T) {
	old, required := EmbeddedBase64, RequireOfficial
	t.Cleanup(func() { EmbeddedBase64 = old; RequireOfficial = required })
	EmbeddedBase64 = ""
	RequireOfficial = ""
	t.Setenv("ZCARD_MARKET_PROFILE", "")
	if p, e := Load(); e != nil || p != nil {
		t.Fatal(p, e)
	}
	RequireOfficial = "true"
	if _, e := Load(); e == nil {
		t.Fatal("missing official profile accepted")
	}
	p := ma.PublicProfile{ProfileID: "official", Origin: "https://market.zcard.dev", LicenseIssuer: "https://market.zcard.dev", DistributionRoots: map[string]string{"distribution": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))}, LicenseRoots: map[string]string{"license": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))}}
	raw, _ := json.Marshal(p)
	EmbeddedBase64 = base64.StdEncoding.EncodeToString(raw)
	if _, e := Load(); e != nil {
		t.Fatal(e)
	}
	for _, origin := range []string{"https://market.example.com", "https://localhost", "http://market.zcard.dev", "https://127.0.0.1", "https://market.zcard.dev/path"} {
		p.Origin = origin
		p.LicenseIssuer = origin
		raw, _ = json.Marshal(p)
		if _, e := Parse(raw, true); e == nil {
			t.Fatal(origin)
		}
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	os.WriteFile(path, []byte(`{}`), 0600)
	t.Setenv("ZCARD_MARKET_PROFILE", path)
	if _, e := Load(); e == nil {
		t.Fatal("bad explicit config fell back silently")
	}
}
