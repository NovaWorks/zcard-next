package migrations_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

func TestUpstreamNavigationUpgradePreservesHistory(t *testing.T) {
	d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: filepath.Join(t.TempDir(), "legacy.db")}})
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
		if strings.HasSuffix(name, "_upstream_navigation.sql") {
			upgrade = sql
			break
		}
		if _, e = d.DB.Exec(string(sql)); e != nil {
			t.Fatal(name, e)
		}
	}
	if len(upgrade) == 0 {
		t.Fatal("missing upgrade")
	}
	for _, sql := range []string{
		`INSERT INTO products(id,created_at,updated_at,name,slug,price,upstream_source_id,upstream_product_code) VALUES(42,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy','legacy',999,9,'CA814E42E563538A')`,
		`INSERT INTO orders(id,created_at,updated_at,order_no,total_amount,status) VALUES(44,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy-order',999,'paid')`,
		`INSERT INTO order_items(id,created_at,updated_at,product_id,order_id,quantity,unit_price,amount,fulfillment_type,upstream_source_snapshot) VALUES(45,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,42,44,1,999,999,'upstream','{"product_code":"CA814E42E563538A","product_id":"CA814E42E563538A"}')`,
	} {
		if _, e := d.DB.Exec(sql); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := d.DB.Exec(string(upgrade)); e != nil {
		t.Fatal(e)
	}
	p := d.Client.Product.GetX(context.Background(), 42)
	if p.Price != 999 || p.UpstreamProductCode != "CA814E42E563538A" || len(p.UpstreamNavigation) > 0 {
		t.Fatal("upgrade rewrote original product", p)
	}
	it := d.Client.OrderItem.GetX(context.Background(), 45)
	if it.UpstreamSourceSnapshot["product_id"] != "CA814E42E563538A" || it.UpstreamSourceSnapshot["page_id"] != nil {
		t.Fatal("upgrade invented a historical page ID", it.UpstreamSourceSnapshot)
	}
}
