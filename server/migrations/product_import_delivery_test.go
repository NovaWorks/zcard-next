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

func TestImportDeliveryUpgradePreservesLegacyOrders(t *testing.T) {
	d, cleanup, e := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: filepath.Join(t.TempDir(), "legacy.db")}})
	if e != nil {
		t.Fatal(e)
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
		if strings.HasSuffix(name, "_product_import_delivery.sql") {
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
	for _, q := range []string{
		`INSERT INTO products(id,created_at,updated_at,name,slug,price,is_locked) VALUES(42,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy','legacy',999,1)`,
		`INSERT INTO orders(id,created_at,updated_at,order_no,total_amount,status) VALUES(44,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy-order',999,'paid')`,
		`INSERT INTO order_items(id,created_at,updated_at,product_id,order_id,quantity,unit_price,amount,fulfillment_type) VALUES(45,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,42,44,1,999,999,'upstream')`,
	} {
		if _, e = d.DB.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = d.DB.Exec(string(upgrade)); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	p := d.Client.Product.GetX(ctx, 42)
	it := d.Client.OrderItem.GetX(ctx, 45)
	if p.CategoryProtected || !p.IsLocked || p.Price != 999 || it.DeliverySourceID != 0 || it.FulfillmentType != "upstream" {
		t.Fatal("legacy rows mutated")
	}
	d.Client.ProductDeliverySource.Create().SetProductID(42).SetCurrentKey("0:42:0").SaveX(ctx)
	d.Client.OrderItem.UpdateOneID(45).SetFulfillmentType("reuse").ExecX(ctx)
}
