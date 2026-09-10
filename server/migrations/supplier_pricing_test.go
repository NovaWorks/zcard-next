package migrations_test

import (
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupplierPricingUpgradePreservesProductPrices(t *testing.T) {
	h, err := db.SQLite.Open(filepath.Join(t.TempDir(), "pricing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	dir, _ := migrations.FS("sqlite")
	files, _ := fs.Glob(dir, "*.sql")
	var upgrade []byte
	for _, f := range files {
		b, err := fs.ReadFile(dir, f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(f, "_supplier_pricing_scope.sql") {
			upgrade = b
			break
		}
		if _, err = h.Exec(string(b)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if len(upgrade) == 0 {
		t.Fatal("missing migration")
	}
	_, err = h.Exec(`INSERT INTO supplier_product_prices (id,created_at,updated_at,supplier_account_id,product_id,sku_id,price) VALUES (1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,7,8,0,123)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Exec(string(upgrade)); err != nil {
		t.Fatal(err)
	}
	var scope string
	var price, cat, discount int
	if err = h.QueryRow("SELECT scope,price,category_id,discount_bps FROM supplier_product_prices WHERE id=1").Scan(&scope, &price, &cat, &discount); err != nil || scope != "product" || price != 123 || cat != 0 || discount != 0 {
		t.Fatal(scope, price, cat, discount, err)
	}
	for _, category := range []int{1, 2} {
		if _, err = h.Exec("INSERT INTO supplier_product_prices (created_at,updated_at,supplier_account_id,product_id,sku_id,price,scope,category_id,discount_bps) VALUES (CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,7,0,0,0,'category',?,9000)", category); err != nil {
			t.Fatal("category uniqueness", err)
		}
	}
}
