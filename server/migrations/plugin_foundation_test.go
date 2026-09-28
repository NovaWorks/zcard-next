package migrations_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

// Use dedicated empty databases: each test replays every historical migration,
// inserts legacy data before P1, then applies generated P1 DDL unchanged.
func TestPluginFoundationUpgrade(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "upgrade.db")
			if driver != "sqlite" {
				source = os.Getenv("ZCARD_P1_UPGRADE_" + strings.ToUpper(driver) + "_DSN")
				if source == "" {
					t.Skip("isolated P1 upgrade database not configured")
				}
			}
			d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: driver, Source: source}})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			d.DB.SetMaxOpenConns(1)
			dir, err := migrations.FS(driver)
			if err != nil {
				t.Fatal(err)
			}
			files, err := fs.Glob(dir, "*.sql")
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, name := range files {
				raw, err := fs.ReadFile(dir, name)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(name, "_plugin_foundation.sql") {
					found = true
					for _, statement := range []string{
						`INSERT INTO products(id,created_at,updated_at,name,slug,price) VALUES(42,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy product','p1-legacy',1299)`,
						`INSERT INTO orders(id,created_at,updated_at,order_no,total_amount) VALUES(43,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'P1-LEGACY',1299)`,
					} {
						if _, err = d.DB.Exec(statement); err != nil {
							t.Fatal(err)
						}
					}
				}
				if _, err = d.DB.Exec(string(raw)); err != nil {
					t.Fatal(name, err)
				}
			}
			if !found {
				t.Fatal("P1 migration missing")
			}
			ctx := context.Background()
			p := d.Client.Product.GetX(ctx, 42)
			o := d.Client.Order.GetX(ctx, 43)
			if p.Price != 1299 || p.PluginRuleRevision != 0 || o.TotalAmount != 1299 || o.RequestFingerprint != "" || len(o.PluginDecisions) != 0 {
				t.Fatal("legacy data changed")
			}
			d.Client.InstalledPlugin.Create().SetPluginID("migration-fixture").SaveX(ctx)
			if _, err = d.Client.InstalledPlugin.Create().SetPluginID("migration-fixture").Save(ctx); err == nil {
				t.Fatal("plugin unique index absent")
			}
			d.Client.PluginRequirement.Create().SetPluginID("migration-fixture").SetSubsiteID(0).SetProductID(42).SetRequired(true).SetRevision(1).SaveX(ctx)
			d.Client.PluginData.Create().SetPluginID("migration-fixture").SetSubsiteID(0).SetEntityType("product").SetEntityID(42).SetKey("config").SetPayload([]byte(`{}`)).SaveX(ctx)
			d.Client.PluginRuleLevelRef.Create().SetPluginID("migration-fixture").SetSubsiteID(0).SetProductID(42).SetLevelID(7).SaveX(ctx)
			d.Client.PluginOperation.Create().SetOperationID("00000000-0000-4000-8000-000000000001").SetPluginID("migration-fixture").SetAction("import").SetRequestSha256(strings.Repeat("a", 64)).SetActorID(1).SetSubsiteID(0).SetExpectedGeneration(0).SetPhase("reconciled").SaveX(ctx)
		})
	}
}
