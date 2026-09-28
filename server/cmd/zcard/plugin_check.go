package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin"
	"github.com/NovaWorks/zcard-next/server/internal/platform/pluginstorage"
)

// Read-only preflight, also used before replacing a container image. No schema
// migration, runtime execution, key creation, or reconciliation is permitted.
func runPluginHostCheck(args []string) error {
	fs := flag.NewFlagSet("plugin-host-check", flag.ContinueOnError)
	dir := fs.String("conf", "configs", "existing instance configuration directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	bc, err := loadBootstrap(*dir)
	if err != nil {
		return err
	}
	if bc.Data == nil {
		return fmt.Errorf("database configuration required")
	}
	root, err := pluginstorage.ResolveRoot(*dir, bc.Data.PluginDataDir)
	if err != nil {
		return err
	}
	bc.Data.PluginDataDir = root
	d, closeDB, err := data.NewData(bc.Data)
	if err != nil {
		return err
	}
	defer closeDB()
	plugin.BuildVersion = orDev(Version)
	p, err := plugin.ProvideFilePackages(bc.Data)
	if err != nil {
		return err
	}
	m := plugin.NewManager(plugin.NewRepo(d, plugin.NewCoordinator(), nil), p, nil)
	if err := m.CheckCoreCompatibility(context.Background()); err != nil {
		return err
	}
	fmt.Println("plugin-host-v1: compatible")
	return nil
}
