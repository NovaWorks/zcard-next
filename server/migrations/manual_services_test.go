package migrations_test

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/migrations"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManualServicesUpgradePreservesHistoricalOrders(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "upgrade.db")
			if driver != "sqlite" {
				source = os.Getenv("ZCARD_SERVICES_UPGRADE_" + strings.ToUpper(driver) + "_DSN")
				if source == "" {
					t.Skip("isolated upgrade DB not configured")
				}
			}
			d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: driver, Source: source}})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			d.DB.SetMaxOpenConns(1)
			dir, _ := migrations.FS(driver)
			files, _ := fs.Glob(dir, "*.sql")
			var upgrade []byte
			for _, name := range files {
				sql, err := fs.ReadFile(dir, name)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(name, "_manual_services_private_levels.sql") {
					upgrade = sql
					break
				}
				if _, err = d.DB.Exec(string(sql)); err != nil {
					t.Fatal(name, err)
				}
			}
			if len(upgrade) == 0 {
				t.Fatal("migration missing")
			}
			for _, sql := range []string{
				`INSERT INTO products(id,created_at,updated_at,name,slug,price) VALUES(42,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy product','legacy-product',999)`,
				`INSERT INTO product_skus(id,created_at,updated_at,name,spec_values,product_id) VALUES(43,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy sku','{}',42)`,
				`INSERT INTO users(id,created_at,updated_at,username) VALUES(44,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy-user')`,
				`INSERT INTO member_levels(id,created_at,updated_at,name,discount) VALUES(45,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy level',9800)`,
				`INSERT INTO orders(id,created_at,updated_at,order_no,total_amount,status) VALUES(46,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'LEGACY-ORDER',999,'delivered')`,
				`INSERT INTO order_items(id,created_at,updated_at,order_id,product_id,sku_id,sku_name,unit_price,quantity,amount,fulfillment_type,fulfillment_status) VALUES(47,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,46,42,43,'legacy sku',999,1,999,'auto','delivered')`,
				`INSERT INTO order_deliveries(id,created_at,updated_at,order_id,item_id,card_id,delivery_token_hash,delivered_mode) VALUES(48,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,46,47,99,'old-token','status')`,
			} {
				if _, err = d.DB.Exec(sql); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = d.DB.Exec(string(upgrade)); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			p, err := d.Client.Product.Query().Where(product.ID(42)).Select(product.FieldID, product.FieldName, product.FieldPrice, product.FieldFulfillmentMode, product.FieldManualStock).Only(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if p.Name != "legacy product" || p.Price != 999 || p.FulfillmentMode != "auto" || p.ManualStock != -1 {
				t.Fatalf("product defaults or old data changed: %+v", p)
			}
			sku := d.Client.ProductSku.GetX(ctx, 43)
			if sku.FulfillmentMode != "follow" {
				t.Fatal("SKU default")
			}
			if d.Client.User.GetX(ctx, 44).ManualLevelID != 0 {
				t.Fatal("user assigned automatically")
			}
			level := d.Client.MemberLevel.GetX(ctx, 45)
			if level.AcquireMode != "auto" || level.DisplayMode != "public" || level.Discount != 9800 {
				t.Fatal("old level changed")
			}
			line := d.Client.OrderItem.GetX(ctx, 47)
			if line.Amount != 999 || line.SkuName != "legacy sku" || line.FulfillmentStatus != "delivered" || len(line.FormAnswers) != 0 {
				t.Fatal("historical item changed")
			}
			result := d.Client.OrderDelivery.GetX(ctx, 48)
			if result.DeliveryTokenHash != "old-token" || result.CardID != 99 || len(result.ServiceContent) != 0 {
				t.Fatal("historical delivery changed")
			}
		})
	}
}
