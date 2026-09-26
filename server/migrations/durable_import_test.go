package migrations_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

func TestDurableImportUpgradePreservesConnectionsAndTasks(t *testing.T) {
	d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: filepath.Join(t.TempDir(), "upgrade.db")}})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	d.DB.SetMaxOpenConns(1)
	dir, _ := migrations.FS("sqlite")
	files, _ := fs.Glob(dir, "*.sql")
	var upgrade []byte
	for _, name := range files {
		sql, err := fs.ReadFile(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, "_durable_product_import.sql") {
			upgrade = sql
			break
		}
		if _, err := d.DB.Exec(string(sql)); err != nil {
			t.Fatal(name, err)
		}
	}
	if len(upgrade) == 0 {
		t.Fatal("missing migration")
	}
	for _, sql := range []string{
		`INSERT INTO supply_connections(id,created_at,updated_at,name,driver,base_url,credentials,settings) VALUES(41,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy','acg_faka','https://example.com',X'01020304','{"category_map":{"cat":7}}')`,
		`INSERT INTO supply_sync_tasks(id,created_at,updated_at,connection_id,mode,status,total_count,created_count,error_context) VALUES(42,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,41,'full','failed',1168,13,'legacy failure')`,
		`INSERT INTO supply_mappings(id,created_at,updated_at,connection_id,upstream_product,up_stock,pricing_override) VALUES(43,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,41,'P',8,'{"last_synced_price":1234}')`,
	} {
		if _, err := d.DB.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.DB.Exec(string(upgrade)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	conn := d.Client.SupplyConnection.Query().Where(supplyconnection.ID(41)).Select("id", "credentials", "sync_lease_token", "sync_lease_until", "settings").OnlyX(ctx)
	task := d.Client.SupplySyncTask.GetX(ctx, 42)
	mapping := d.Client.SupplyMapping.Query().Where(supplymapping.ID(43)).Select("id", "up_stock", "pricing_override").OnlyX(ctx)
	if string(conn.Credentials) != string([]byte{1, 2, 3, 4}) || conn.SyncLeaseToken != "" || conn.SyncLeaseUntil != 0 || len(conn.Settings) != 1 {
		t.Fatal("connection was changed by migration")
	}
	if task.RequestKey != nil || len(task.ImportPayload) != 0 || task.TotalCount != 1168 || task.CreatedCount != 13 || task.ErrorContext != "legacy failure" {
		t.Fatal("legacy task changed")
	}
	if mapping.UpStock != 8 || mapping.PricingOverride["last_synced_price"] != float64(1234) {
		t.Fatal("mapping lost")
	}
	d.Client.SupplyImportItem.Create().SetTaskID(42).SetName("new checkpoint").SetCode("P").SetSnapshot(json.RawMessage(`{}`)).ExecX(ctx)
}
