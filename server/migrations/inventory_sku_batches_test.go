package migrations_test

import (
	"database/sql"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

func TestInventorySkuBatchUpgradePreservesUnknownOrigin(t *testing.T) {
	h, err := db.SQLite.Open(filepath.Join(t.TempDir(), "inventory-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.SetMaxOpenConns(1)
	dir, _ := migrations.FS("sqlite")
	files, _ := fs.Glob(dir, "*.sql")
	var upgrade string
	for _, name := range files {
		raw, err := fs.ReadFile(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, "_inventory_sku_batches.sql") {
			upgrade = string(raw)
			break
		}
		if _, err := h.Exec(string(raw)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if upgrade == "" || strings.Contains(strings.ToUpper(upgrade), "DROP ") || strings.Contains(upgrade, "UPDATE ") {
		t.Fatal("batch migration must add metadata without rewriting historical stock")
	}
	for _, statement := range []string{
		`INSERT INTO products(id,created_at,updated_at,name,slug,status,price) VALUES(41,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'old product','old-product',1,100)`,
		`INSERT INTO card_imports(id,created_at,updated_at,product_id,filename,total,imported,status) VALUES(42,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,41,'old.txt',2,2,'done')`,
		`INSERT INTO cards(id,created_at,updated_at,product_id,content,content_hash,status,import_id) VALUES(51,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,41,X'010203','old-card-one','available',42)`,
		`INSERT INTO cards(id,created_at,updated_at,product_id,content,content_hash,status,import_id,sku_id) VALUES(52,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,41,X'040506','old-card-two','used',42,77)`,
	} {
		if _, err := h.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Exec(upgrade); err != nil {
		t.Fatal(err)
	}
	var target sql.NullInt64
	var name string
	if err := h.QueryRow(`SELECT sku_id,sku_name FROM card_imports WHERE id=42`).Scan(&target, &name); err != nil || target.Valid || name != "" {
		t.Fatalf("old origin must stay unknown: %+v %q %v", target, name, err)
	}
	var cards int
	if err := h.QueryRow(`SELECT COUNT(*) FROM cards WHERE import_id=42 AND ((id=51 AND sku_id IS NULL AND status='available' AND hex(content)='010203') OR (id=52 AND sku_id=77 AND status='used' AND hex(content)='040506'))`).Scan(&cards); err != nil || cards != 2 {
		t.Fatalf("stock or provenance changed: %d %v", cards, err)
	}
	if _, err := h.Exec(`INSERT INTO card_imports(id,created_at,updated_at,product_id,filename,sku_id,sku_name) VALUES(43,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,41,'default.txt',0,'默认规格')`); err != nil {
		t.Fatal(err)
	}
	if err := h.QueryRow(`SELECT sku_id FROM card_imports WHERE id=43`).Scan(&target); err != nil || !target.Valid || target.Int64 != 0 {
		t.Fatalf("new explicit default must differ from historical unknown: %+v %v", target, err)
	}
}
