package plugin

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/google/wire"
)

// BuildVersion is supplied once by the executable before Wire constructs services.
var BuildVersion = "dev"
var SplitMode bool
var ProviderSet = wire.NewSet(NewCoordinator, NewRepo, ProvideFilePackages, ProvideManager, NewAdminPluginService, NewRequiredGate, wire.Bind(new(port.PurchaseGate), new(*RequiredGate)),
	wire.Bind(new(port.RequirementStore), new(*Repo)), wire.Bind(new(port.ConfigStore), new(*Repo)))

func ProvideFilePackages(c *conf.Data) (*FilePackages, error) {
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
	return NewFilePackages(filepath.Join(c.PluginDataDir, "plugins"), keys, pc.Host{CoreVersion: strings.TrimPrefix(BuildVersion, "v"), APIVersion: "1", Capabilities: map[string]bool{pc.HookOrderPreCreate: true}, UIExtensions: map[string]bool{pc.ProductEditor: true}})
}
func ProvideManager(r *Repo, p *FilePackages) *Manager {
	r.coordinator.readOnly = SplitMode
	return NewManager(r, p, NewRuntimeLoader(p))
}
