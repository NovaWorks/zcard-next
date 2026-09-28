package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ProtectPluginHost persists the configuration location before the new host
// serves. All supported replacement paths (including automatic rollback) must
// ask the candidate to validate the installed v1 contracts before touching files.
// Keep this marker with binary backups. Removing it is not a supported downgrade.
func ProtectPluginHost(binaryPath, configDir string) error {
	configDir, err := filepath.Abs(configDir)
	if err != nil {
		return err
	}
	path := binaryPath + ".plugin-host"
	if old, e := os.ReadFile(path); e == nil && string(old) == configDir {
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".plugin-host-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(configDir); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func checkPluginHost(binaryPath, candidate string) error {
	raw, err := os.ReadFile(binaryPath + ".plugin-host")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	configDir := string(raw)
	if !filepath.IsAbs(configDir) || strings.ContainsAny(configDir, "\r\n") {
		return fmt.Errorf("updater: invalid plugin host guard")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Candidate was already signature/hash verified by the download path. Its
	// preflight only reads DB and signed artifacts, without migration or serving.
	cmd := exec.CommandContext(ctx, candidate, "plugin-host-check", "-conf", configDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("updater: candidate cannot preserve plugin requirements; run plugin-host-check before replacement: %w", err)
	}
	return nil
}
