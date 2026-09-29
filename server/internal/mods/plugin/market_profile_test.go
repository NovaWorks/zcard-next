package plugin

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	ma "github.com/NovaWorks/zcard-next/server/internal/platform/marketaccount"
	"os"
	"path/filepath"
	"testing"
)

func TestOfficialProfilePrecedenceAndTrust(t *testing.T) {
	p := ma.PublicProfile{ProfileID: "official", Origin: "https://market.zcard.dev", LicenseIssuer: "https://market.zcard.dev", DistributionRoots: map[string]string{"official-dist": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))}, LicenseRoots: map[string]string{"official-license": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))}}
	raw, _ := json.Marshal(p)
	path := filepath.Join(t.TempDir(), "profile.json")
	os.WriteFile(path, raw, 0600)
	t.Setenv("ZCARD_MARKET_PROFILE", path)
	input := &conf.Data{PluginLicenseIssuer: "https://custom.example", PluginTrustedKeys: map[string]string{"custom": "original"}}
	cfg, profile, e := marketConfiguration(input)
	if e != nil || cfg.PluginLicenseIssuer != input.PluginLicenseIssuer || cfg.PluginTrustedKeys["custom"] != "original" || len(input.PluginTrustedKeys) != 1 {
		t.Fatal(cfg, e)
	}
	input.PluginTrustedKeys["official-dist"] = "conflicting"
	if _, _, e = marketConfiguration(input); e == nil {
		t.Fatal("trust overwritten")
	}
	s, d, ctx, _ := serviceFixture(t)
	m := s.manager
	m.officialProfile = profile
	// The fixture imported one completed local package; that must not suppress the new default.
	d.Client.PluginOperation.Delete().ExecX(ctx)
	if e = m.applyOfficialOrigin(ctx); e != nil {
		t.Fatal(e)
	}
	origin, e := m.repo.marketOrigin(ctx)
	if e != nil || origin != p.Origin {
		t.Fatal(origin, e)
	}
	m.repo.configureMarket(ctx, "https://custom.example")
	if e = m.applyOfficialOrigin(ctx); e != nil {
		t.Fatal(e)
	}
	origin, _ = m.repo.marketOrigin(ctx)
	if origin != "https://custom.example" {
		t.Fatal("custom replaced")
	}
	m.repo.configureMarket(ctx, "")
	m.applyOfficialOrigin(ctx)
	origin, _ = m.repo.marketOrigin(ctx)
	if origin != "" {
		t.Fatal("explicit offline configuration replaced")
	}
}

func TestDefaultMarketWithoutTrustProfile(t *testing.T) {
	s, d, ctx, _ := serviceFixture(t)
	d.Client.PluginOperation.Delete().ExecX(ctx)
	s.manager.officialProfile = nil
	if err := s.manager.applyOfficialOrigin(ctx); err != nil {
		t.Fatal(err)
	}
	origin, err := s.manager.repo.marketOrigin(ctx)
	if err != nil || origin != "https://store.zcard.dev" {
		t.Fatal(origin, err)
	}
}
