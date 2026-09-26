package migrations_test

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/migrations"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestLowStockUpgradePreservesMappings(t *testing.T) {
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
		sql, e := fs.ReadFile(dir, name)
		if e != nil {
			t.Fatal(e)
		}
		if strings.HasSuffix(name, "_low_stock_monitor.sql") {
			upgrade = sql
			break
		}
		if _, e = d.DB.Exec(string(sql)); e != nil {
			t.Fatal(name, e)
		}
	}
	if len(upgrade) == 0 {
		t.Fatal("missing migration")
	}
	for _, sql := range []string{
		`INSERT INTO supply_connections(id,created_at,updated_at,name,driver,base_url,credentials,settings) VALUES(1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'existing','zcard','https://example.invalid',X'0102','{"schedule":{"price":{"enabled":true,"interval":30}}}')`,
		`INSERT INTO supply_mappings(id,created_at,updated_at,connection_id,upstream_product,local_product_id,up_stock,stock_reference,stock_checked_at) VALUES(2,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,1,'old',3,2,2,CURRENT_TIMESTAMP)`,
	} {
		if _, err = d.DB.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = d.DB.Exec(string(upgrade)); err != nil {
		t.Fatal(err)
	}
	m := d.Client.SupplyMapping.GetX(context.Background(), 2)
	if m.UpStock != 2 || m.StockReference != 2 || m.LocalProductID != 3 || m.StockProbeAfter != 0 || m.StockProbeLease != 0 {
		t.Fatal(m)
	}
	c := d.Client.SupplyConnection.GetX(context.Background(), 1)
	if c.Name != "existing" || len(c.Credentials) != 2 || c.Settings["schedule"] == nil || c.LowStockScannedAt != 0 {
		t.Fatal("connection changed")
	}
	d.Client.StockAlert.Create().SetProductID(3).SetSkuID(0).SetSourceKey("upstream").SaveX(context.Background())
}
