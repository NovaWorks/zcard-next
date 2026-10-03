package data

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	atlasmigrate "ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/sqlclient"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

func TestPhysicalRefinementUpgradePreservesCommerceAndPreferences(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "physical-refinement.db")
	d, cleanup, err := NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: dsn}})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	physicalRefinementUpgradePreservesCommerceAndPreferences(t, d, db.SQLite, dsn)
}

func physicalRefinementUpgradePreservesCommerceAndPreferences(t *testing.T, d *Data, dialect db.Dialect, dsn string) {
	t.Helper()
	ctx := context.Background()
	fsys, err := migrations.FS(string(dialect))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := embedToMemDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	files, err := dir.Files()
	if err != nil {
		t.Fatal(err)
	}
	before := -1
	for i, f := range files {
		if strings.Contains(f.Name(), "physical_commerce_refinement") {
			before = i
			break
		}
	}
	if before < 1 {
		t.Fatal("physical refinement migration missing")
	}
	var driver atlasmigrate.Driver
	revisionDB := d.DB
	if dialect == db.Postgres {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		client, err := sqlclient.OpenURL(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		driver, revisionDB = client, client.DB
	} else {
		driver, err = atlasDriver(d.DB, dialect)
		if err != nil {
			t.Fatal(err)
		}
	}
	revs, err := newRevisionReadWriter(revisionDB, dialect)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := atlasmigrate.NewExecutor(driver, dir, revs)
	if err != nil {
		t.Fatal(err)
	}
	if err = executor.ExecuteN(ctx, before); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO products(id,created_at,updated_at,name,slug,price,goods_type,physical_stock,stock_visible,shipping_countries) VALUES(142,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'Physical legacy','physical-legacy',1200,'physical',11,false,'["US"]')`,
		`INSERT INTO products(id,created_at,updated_at,name,slug,price,goods_type,stock_visible) VALUES(143,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'Virtual legacy','virtual-legacy',999,'virtual',true)`,
		`INSERT INTO product_skus(id,created_at,updated_at,name,spec_values,product_id,physical_stock) VALUES(144,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'Legacy size','{}',142,7)`,
		`INSERT INTO orders(id,created_at,updated_at,order_no,total_amount,status,commerce_version,query_password_hash) VALUES(146,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'REFINEMENT-PHYSICAL',2400,'delivered',1,'legacy-query-hash')`,
		`INSERT INTO orders(id,created_at,updated_at,order_no,total_amount,status,commerce_version) VALUES(147,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'REFINEMENT-VIRTUAL',999,'delivered',0)`,
		`INSERT INTO order_items(id,created_at,updated_at,order_id,product_id,sku_id,unit_price,quantity,amount,paid_amount,goods_type,fulfillment_type,fulfillment_status,shipped_quantity,received_quantity,returned_quantity) VALUES(148,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,146,142,144,1200,2,2400,2400,'physical','shipping','delivered',2,1,1)`,
		`INSERT INTO order_items(id,created_at,updated_at,order_id,product_id,unit_price,quantity,amount,goods_type,fulfillment_type,fulfillment_status) VALUES(149,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,147,143,999,1,999,'virtual','auto','delivered')`,
	} {
		if _, err = d.DB.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err = ApplyMigrations(ctx, d.DB, dialect, dsn); err != nil {
		t.Fatal(err)
	}
	physical := d.Client.Product.GetX(ctx, 142)
	virtual := d.Client.Product.GetX(ctx, 143)
	sku := d.Client.ProductSku.GetX(ctx, 144)
	o := d.Client.Order.GetX(ctx, 146)
	vo := d.Client.Order.GetX(ctx, 147)
	item := d.Client.OrderItem.GetX(ctx, 148)
	virtualItem := d.Client.OrderItem.GetX(ctx, 149)
	if physical.ProductProperty != "physical" || physical.GoodsType != "physical" || physical.PhysicalStock != 11 || physical.StockVisible || !physical.TrackInventory || !physical.SalesVisible || physical.Price != 1200 {
		t.Fatalf("physical product changed or failed property backfill: %+v", physical)
	}
	if virtual.ProductProperty != "virtual" || virtual.GoodsType != "virtual" || !virtual.StockVisible || !virtual.TrackInventory || !virtual.SalesVisible || virtual.Price != 999 || sku.PhysicalStock != 7 {
		t.Fatal("legacy virtual product, SKU stock or display preference changed")
	}
	if o.CommerceVersion != 1 || o.Status != "delivered" || o.TotalAmount != 2400 || o.QueryPasswordHash != "legacy-query-hash" || o.OrderAccessTokenHash != "" || vo.CommerceVersion != 0 || vo.TotalAmount != 999 {
		t.Fatal("historical order totals, credentials or version changed")
	}
	if !item.InventoryTracked || item.Amount != 2400 || item.PaidAmount != 2400 || item.ShippedQuantity != 2 || item.ReceivedQuantity != 1 || item.ReturnedQuantity != 1 || virtualItem.Amount != 999 || virtualItem.FulfillmentType != "auto" {
		t.Fatal("historical fulfillment or tracked-inventory snapshot changed")
	}
	// Applying migrations again must keep preferences saved after the upgrade.
	d.Client.Product.UpdateOneID(physical.ID).SetTrackInventory(false).SetSalesVisible(false).ExecX(ctx)
	if err = ApplyMigrations(ctx, d.DB, dialect, dsn); err != nil {
		t.Fatal(err)
	}
	physical = d.Client.Product.GetX(ctx, physical.ID)
	if physical.ProductProperty != "physical" || physical.TrackInventory || physical.SalesVisible || physical.StockVisible || physical.PhysicalStock != 11 || d.Client.ProductSku.GetX(ctx, sku.ID).PhysicalStock != 7 || d.Client.Order.GetX(ctx, o.ID).TotalAmount != 2400 || !d.Client.OrderItem.GetX(ctx, item.ID).InventoryTracked {
		t.Fatal("repeated migration reset settings or historical commerce")
	}
}

func TestProductPropertyStorageMirror(t *testing.T) {
	ctx := context.Background()
	d, cleanup, err := NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: "file:" + filepath.Join(t.TempDir(), "property.db")}})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err = d.Client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	canonical := d.Client.Product.Create().SetName("Canonical physical").SetSlug("canonical-physical").SetProductProperty("physical").SaveX(ctx)
	legacy := d.Client.Product.Create().SetName("Legacy physical").SetSlug("legacy-physical").SetGoodsType("physical").SaveX(ctx)
	for _, p := range []uint64{canonical.ID, legacy.ID} {
		row := d.Client.Product.GetX(ctx, p)
		if row.ProductProperty != "physical" || row.GoodsType != "physical" {
			t.Fatal("single-field physical creation did not mirror properties")
		}
	}
	if _, err = d.Client.Product.UpdateOneID(canonical.ID).SetProductProperty("physical").SetGoodsType("virtual").Save(ctx); err == nil {
		t.Fatal("explicit update conflict accepted")
	}
	row := d.Client.Product.GetX(ctx, canonical.ID)
	if row.ProductProperty != "physical" || row.GoodsType != "physical" {
		t.Fatal("conflicting update partially changed product")
	}
	// The storage hook supports internal legacy writers; the catalog API separately
	// prevents changing a saved product's physical/virtual classification.
	row = d.Client.Product.UpdateOneID(canonical.ID).SetProductProperty("virtual").SaveX(ctx)
	if row.GoodsType != "virtual" || row.ProductProperty != "virtual" {
		t.Fatal("canonical-only update did not synchronize legacy field")
	}
	row = d.Client.Product.UpdateOneID(canonical.ID).SetGoodsType("physical").SaveX(ctx)
	if row.GoodsType != "physical" || row.ProductProperty != "physical" {
		t.Fatal("legacy-only update did not synchronize canonical property")
	}
}
