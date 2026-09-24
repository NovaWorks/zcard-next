package migrations_test

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/migrations"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductLockUpgradePreservesProductsAndChildren(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "upgrade.db")
			if driver != "sqlite" {
				source = os.Getenv("ZCARD_LOCK_UPGRADE_" + strings.ToUpper(driver) + "_DSN")
				if source == "" {
					t.Skip("isolated upgrade database not configured")
				}
			}
			d, cleanup, e := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: driver, Source: source}})
			if e != nil {
				t.Fatal(e)
			}
			defer cleanup()
			d.DB.SetMaxOpenConns(1)
			dir, _ := migrations.FS(driver)
			files, _ := fs.Glob(dir, "*.sql")
			var upgrade []byte
			for _, name := range files {
				sql, e := fs.ReadFile(dir, name)
				if e != nil {
					t.Fatal(e)
				}
				if strings.HasSuffix(name, "_product_lock_placements.sql") {
					upgrade = sql
					break
				}
				if _, e = d.DB.Exec(string(sql)); e != nil {
					t.Fatal(name, e)
				}
			}
			if len(upgrade) == 0 {
				t.Fatal("migration missing")
			}
			for _, sql := range []string{
				`INSERT INTO categories(id,created_at,updated_at,name) VALUES(40,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy category')`,
				`INSERT INTO products(id,created_at,updated_at,name,slug,price,category_id) VALUES(42,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy product','legacy-product',999,40)`,
				`INSERT INTO product_skus(id,created_at,updated_at,name,spec_values,product_id) VALUES(43,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy sku','{}',42)`,
				`INSERT INTO product_controls(id,created_at,updated_at,name,type,product_id) VALUES(44,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy field','text',42)`,
			} {
				if _, e = d.DB.Exec(sql); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = d.DB.Exec(string(upgrade)); e != nil {
				t.Fatal(e)
			}
			ctx := context.Background()
			p := d.Client.Product.GetX(ctx, 42)
			if p.IsLocked || p.LockVersion != 0 || p.LockedAt != nil || p.Price != 999 || p.CategoryID != 40 {
				t.Fatalf("old product changed: %+v", p)
			}
			if d.Client.ProductSku.GetX(ctx, 43).ProductID != 42 || d.Client.ProductControl.GetX(ctx, 44).ProductID != 42 {
				t.Fatal("child lost")
			}
			if d.Client.Category.GetX(ctx, 40).PlacementVersion != 0 {
				t.Fatal("category default")
			}
			d.Client.CategoryProductPlacement.Create().SetCategoryID(40).SetProductID(42).SetIsPinned(true).ExecX(ctx)
		})
	}
}
