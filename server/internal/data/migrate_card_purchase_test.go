package data

import (
	"bytes"
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	mysqldriver "github.com/go-sql-driver/mysql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	atlasmigrate "ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/sqlclient"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

func TestCardPurchaseMigrationsSQLite(t *testing.T)   { cardPurchaseMigrations(t, db.SQLite) }
func TestCardPurchaseMigrationsMySQL(t *testing.T)    { cardPurchaseMigrations(t, db.MySQL) }
func TestCardPurchaseMigrationsPostgres(t *testing.T) { cardPurchaseMigrations(t, db.Postgres) }
func cardPurchaseMigrations(t *testing.T, dialect db.Dialect) {
	for _, upgrade := range []bool{false, true} {
		label := "fresh"
		if upgrade {
			label = "upgrade"
		}
		t.Run(label, func(t *testing.T) {
			d, dsn := cardPurchaseStore(t, dialect)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			fsys, e := migrations.FS(string(dialect))
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
				if strings.Contains(f.Name(), "tghao_card_purchase") {
					before = i
					break
				}
			}
			if before < 1 {
				t.Fatal("card purchase migration missing")
			}
			if upgrade {
				driver, e := atlasDriver(d.DB, dialect)
				if e != nil {
					t.Fatal(e)
				}
				if dialect == db.Postgres {
					client, e := sqlclient.Open(ctx, dsn)
					if e != nil {
						t.Fatal(e)
					}
					t.Cleanup(func() { client.Close() })
					driver = client
				}
				revisions, e := newRevisionReadWriter(d.DB, dialect)
				if e != nil {
					t.Fatal(e)
				}
				executor, e := atlasmigrate.NewExecutor(driver, dir, revisions)
				if e != nil {
					t.Fatal(e)
				}
				if e = executor.ExecuteN(ctx, before); e != nil {
					t.Fatal("pre-card baseline migration", e)
				}
				for _, sql := range []string{
					`INSERT INTO products(id,created_at,updated_at,name,slug,price,goods_type) VALUES(9001,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy card','legacy-card-quote',1001,'virtual')`,
					`INSERT INTO orders(id,created_at,updated_at,order_no,total_amount,status,query_password_hash) VALUES(9002,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'LEGACY-CARD-QUOTE',3003,'paid','legacy-query-hash')`,
					`INSERT INTO order_items(id,created_at,updated_at,order_id,product_id,unit_price,quantity,amount,fulfillment_type,goods_type,social_purchase_snapshot) VALUES(9003,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,9002,9001,1001,3,3003,'upstream','virtual','')`,
				} {
					if _, e = d.DB.ExecContext(ctx, sql); e != nil {
						t.Fatal("pre-card fixture", e)
					}
				}
			}
			if e = ApplyMigrations(ctx, d.DB, dialect, dsn); e != nil {
				t.Fatal("actual card migration", e)
			}
			if upgrade {
				p := d.Client.Product.GetX(ctx, 9001)
				o := d.Client.Order.GetX(ctx, 9002)
				it := d.Client.OrderItem.GetX(ctx, 9003)
				if p.Price != 1001 || o.TotalAmount != 3003 || it.UnitPrice != 1001 || it.Quantity != 3 || it.Amount != 3003 || len(it.UpstreamCardCipher) != 0 {
					t.Fatal("card migration changed legacy money or purchase", p, o, it)
				}
			}
			if upgrade {
				cipher := []byte{0, 255, 0, 7}
				d.Client.OrderItem.UpdateOneID(9003).SetUpstreamCardCipher(cipher).ExecX(ctx)
				if !bytes.Equal(d.Client.OrderItem.GetX(ctx, 9003).UpstreamCardCipher, cipher) {
					t.Fatal("migrated frozen card cipher corrupted")
				}
			} else {
				rows, e := d.DB.QueryContext(ctx, "SELECT upstream_card_cipher FROM order_items LIMIT 1")
				if e != nil {
					t.Fatal("frozen card cipher column missing", e)
				}
				rows.Close()
			}
			ctrl := d.Client.ProductControl.Create().SetProductID(9001).SetName("Choice").SetType("select").SetOptions([]string{"basic"}).SetOptionLabels(map[string]string{"basic": "基础方案"}).SaveX(ctx)
			if d.Client.ProductControl.GetX(ctx, ctrl.ID).OptionLabels["basic"] != "基础方案" {
				t.Fatal("migrated option labels corrupted")
			}
			var count int
			if e = d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM atlas_schema_revisions").Scan(&count); e != nil || count != len(files) {
				t.Fatal("migration ledger incomplete", count, len(files), e)
			}
			if e = ApplyMigrations(ctx, d.DB, dialect, dsn); e != nil {
				t.Fatal("repeat migration", e)
			}
			var repeated int
			if e = d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM atlas_schema_revisions").Scan(&repeated); e != nil || repeated != count {
				t.Fatal("repeat migration changed revisions", repeated, count, e)
			}
		})
	}
}

func cardPurchaseStore(t *testing.T, dialect db.Dialect) (*Data, string) {
	t.Helper()
	ctx := context.Background()
	target := fmt.Sprintf("zcard_card_upgrade_%d", time.Now().UnixNano())
	var dsn string
	switch dialect {
	case db.SQLite:
		dsn = "file:" + filepath.Join(t.TempDir(), "upgrade.sqlite")
	case db.MySQL, db.Postgres:
		env := "ZCARD_TEST_MYSQL_DSN"
		if dialect == db.Postgres {
			env = "ZCARD_TEST_PG_DSN"
		}
		adminDSN := os.Getenv(env)
		if adminDSN == "" {
			t.Skip(env + " not configured")
		}
		admin, e := dialect.Open(adminDSN)
		if e != nil {
			t.Fatal(e)
		}
		if dialect == db.MySQL {
			if _, e = admin.ExecContext(ctx, "CREATE DATABASE "+target+" CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); e != nil {
				admin.Close()
				t.Fatal(e)
			}
			cfg, e := mysqldriver.ParseDSN(adminDSN)
			if e != nil {
				t.Fatal(e)
			}
			cfg.DBName = target
			dsn = cfg.FormatDSN()
			t.Cleanup(func() { admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+target); admin.Close() })
		} else {
			if _, e = admin.ExecContext(ctx, "CREATE SCHEMA "+target); e != nil {
				admin.Close()
				t.Fatal(e)
			}
			u, e := url.Parse(adminDSN)
			if e != nil {
				t.Fatal(e)
			}
			q := u.Query()
			q.Set("search_path", target)
			u.RawQuery = q.Encode()
			dsn = u.String()
			t.Cleanup(func() {
				admin.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+target+" CASCADE")
				admin.Close()
			})
		}
	}
	d, closeDB, e := NewData(&conf.Data{Database: &conf.Data_Database{Driver: string(dialect), Source: dsn, MaxOpenConns: 5, MaxIdleConns: 5}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(closeDB)
	return d, dsn
}
