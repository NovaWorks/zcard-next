package data

import (
	atlasmigrate "ariga.io/atlas/sql/migrate"
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhysicalUpgradePreservesLegacyCommerce(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.Join(t.TempDir(), "physical.db")
	d, closeDB, e := NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: dsn}})
	if e != nil {
		t.Fatal(e)
	}
	defer closeDB()
	fsys, e := migrations.FS("sqlite")
	if e != nil {
		t.Fatal(e)
	}
	dir, e := embedToMemDir(fsys)
	if e != nil {
		t.Fatal(e)
	}
	files, e := dir.Files()
	if e != nil {
		t.Fatal(e)
	}
	before := -1
	for i, f := range files {
		if strings.Contains(f.Name(), "physical_commerce") {
			before = i
			break
		}
	}
	if before < 1 {
		t.Fatal("physical migration missing")
	}
	driver, e := atlasDriver(d.DB, db.SQLite)
	if e != nil {
		t.Fatal(e)
	}
	revs, e := newRevisionReadWriter(d.DB, db.SQLite)
	if e != nil {
		t.Fatal(e)
	}
	ex, e := atlasmigrate.NewExecutor(driver, dir, revs)
	if e != nil {
		t.Fatal(e)
	}
	if e = ex.ExecuteN(ctx, before); e != nil {
		t.Fatal(e)
	}
	for _, sql := range []string{
		`INSERT INTO products(id,created_at,updated_at,name,slug,price,fulfillment_mode) VALUES(42,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy','legacy',999,'manual')`,
		`INSERT INTO product_skus(id,created_at,updated_at,name,spec_values,product_id) VALUES(43,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy sku','{}',42)`,
		`INSERT INTO orders(id,created_at,updated_at,order_no,total_amount,status) VALUES(46,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'LEGACY-PHYSICAL-UPGRADE',999,'delivered')`,
		`INSERT INTO order_items(id,created_at,updated_at,order_id,product_id,sku_id,unit_price,quantity,amount,fulfillment_type,fulfillment_status) VALUES(47,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,46,42,43,999,1,999,'manual','delivered')`,
		`INSERT INTO order_deliveries(id,created_at,updated_at,order_id,item_id,card_id,delivery_token_hash,delivered_mode) VALUES(48,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,46,47,99,'legacy-token','status')`,
	} {
		if _, e = d.DB.ExecContext(ctx, sql); e != nil {
			t.Fatal(e)
		}
	}
	if e = ApplyMigrations(ctx, d.DB, db.SQLite, dsn); e != nil {
		t.Fatal(e)
	}
	if e = ApplyMigrations(ctx, d.DB, db.SQLite, dsn); e != nil {
		t.Fatal(e)
	}
	p := d.Client.Product.GetX(ctx, 42)
	o := d.Client.Order.GetX(ctx, 46)
	it := d.Client.OrderItem.GetX(ctx, 47)
	sku := d.Client.ProductSku.GetX(ctx, 43)
	delivery := d.Client.OrderDelivery.GetX(ctx, 48)
	if p.GoodsType != "virtual" || p.FulfillmentMode != "manual" || p.Price != 999 || o.CommerceVersion != 0 || o.Status != "delivered" || o.TotalAmount != 999 || o.ShippingAmount != 0 || it.Amount != 999 || it.FulfillmentType != "manual" || it.GoodsType != "virtual" || sku.PhysicalStock != 0 || delivery.DeliveryTokenHash != "legacy-token" {
		t.Fatal("historical commerce changed during upgrade")
	}
}
