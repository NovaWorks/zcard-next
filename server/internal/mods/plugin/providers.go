package plugin

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	lp "github.com/NovaWorks/zcard-next/server/internal/mods/license/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	ma "github.com/NovaWorks/zcard-next/server/internal/platform/marketaccount"
	"github.com/NovaWorks/zcard-next/server/internal/platform/marketprofile"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/google/wire"
	"google.golang.org/protobuf/proto"
)

// BuildVersion is supplied once by the executable before Wire constructs services.
var BuildVersion = "dev"
var SplitMode bool
var ProviderSet = wire.NewSet(NewCoordinator, NewRepo, ProvideFilePackages, ProvideManager, NewAdminPluginService, NewRequiredGate, wire.Bind(new(port.PurchaseGate), new(*RequiredGate)),
	wire.Bind(new(port.RequirementStore), new(*Repo)), wire.Bind(new(port.ConfigStore), new(*Repo)))

func ProvideFilePackages(c *conf.Data) (*FilePackages, error) {
	c, _, err := marketConfiguration(c)
	if err != nil {
		return nil, err
	}
	if c.PluginDataDir == "" || !filepath.IsAbs(c.PluginDataDir) {
		return nil, fmt.Errorf("plugin data root must be explicitly resolved")
	}
	if len(c.PluginTrustedKeys) > 32 {
		return nil, fmt.Errorf("too many plugin trust keys")
	}
	keys := map[string]ed25519.PublicKey{}
	for id, encoded := range c.PluginTrustedKeys {
		if len(id) > 64 || !pluginIDPattern.MatchString(id) {
			return nil, fmt.Errorf("invalid plugin trust key ID")
		}
		b, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("invalid plugin public key")
		}
		keys[id] = ed25519.PublicKey(b)
	}
	return NewFilePackages(filepath.Join(c.PluginDataDir, "plugins"), keys, pc.Host{PaidEntitlements: true, CoreVersion: strings.TrimPrefix(BuildVersion, "v"), APIVersion: "1", Capabilities: map[string]bool{pc.HookOrderPreCreate: true}, UIExtensions: map[string]bool{pc.ProductEditor: true}})
}
func ProvideManager(r *Repo, p *FilePackages, cfg *conf.Data, identity lp.InstanceIdentity, box *crypto.Box) (*Manager, error) {
	cfg, profile, err := marketConfiguration(cfg)
	if err != nil {
		return nil, err
	}
	r.coordinator.readOnly = SplitMode
	m := NewManager(r, p, NewRuntimeLoader(p))
	policy, err := newLicensePolicy(cfg, identity)
	if err != nil {
		return nil, err
	}
	m.licensing = policy
	m.bindingBox = box
	m.officialProfile = profile
	return m, nil
}

// Explicit deployment values win; a key ID collision must never silently replace trust.
func marketConfiguration(input *conf.Data) (*conf.Data, *ma.PublicProfile, error) {
	p, err := marketprofile.Load()
	if err != nil {
		return nil, nil, err
	}
	c := proto.Clone(input).(*conf.Data)
	if p == nil {
		return c, nil, nil
	}
	if c.PluginTrustedKeys == nil {
		c.PluginTrustedKeys = map[string]string{}
	}
	for id, key := range p.DistributionRoots {
		if old, ok := c.PluginTrustedKeys[id]; ok && old != key {
			return nil, nil, fmt.Errorf("conflicting market distribution root")
		}
		c.PluginTrustedKeys[id] = key
	}
	if c.PluginLicenseRoots == nil {
		c.PluginLicenseRoots = map[string]*conf.Data_PluginLicenseRoot{}
	}
	for id, key := range p.LicenseRoots {
		if old, ok := c.PluginLicenseRoots[id]; ok && (old == nil || old.PublicKey != key || old.Issuer != p.LicenseIssuer) {
			return nil, nil, fmt.Errorf("conflicting market license root")
		}
		c.PluginLicenseRoots[id] = &conf.Data_PluginLicenseRoot{Issuer: p.LicenseIssuer, PublicKey: key}
	}
	if c.PluginLicenseIssuer == "" {
		c.PluginLicenseIssuer = p.LicenseIssuer
	}
	return c, p, nil
}
