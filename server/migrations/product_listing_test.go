package migrations_test

import (
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestListingSQLiteUpgradePreservesManualState(t *testing.T) {
	handle, e := db.SQLite.Open(filepath.Join(t.TempDir(), "upgrade.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer handle.Close()
	handle.SetMaxOpenConns(1)
	dir, _ := migrations.FS("sqlite")
	files, _ := fs.Glob(dir, "*.sql")
	var upgrade []byte
	for _, name := range files {
		sql, e := fs.ReadFile(dir, name)
		if e != nil {
			t.Fatal(e)
		}
		if strings.HasSuffix(name, "_product_listing.sql") {
			upgrade = sql
			break
		}
		if _, e = handle.Exec(string(sql)); e != nil {
			t.Fatalf("%s: %v", name, e)
		}
	}
	if len(upgrade) == 0 || strings.Contains(string(upgrade), "DROP TABLE") {
		t.Fatal("listing migration must only add columns")
	}
	_, e = handle.Exec(`INSERT INTO products(id,created_at,updated_at,name,slug,status,is_locked,price) VALUES(41,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'manual-off','manual-off',0,1,100),(42,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'hidden','hidden',2,0,200)`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = handle.Exec(string(upgrade)); e != nil {
		t.Fatal(e)
	}
	var status, price int
	var locked, automatic bool
	var reason string
	e = handle.QueryRow(`SELECT status,is_locked,price,auto_listing,listing_reason FROM products WHERE id=41`).Scan(&status, &locked, &price, &automatic, &reason)
	if e != nil || status != 0 || !locked || price != 100 || automatic || reason != "" {
		t.Fatal("existing manual-off product changed", e)
	}
	e = handle.QueryRow(`SELECT status,auto_listing FROM products WHERE id=42`).Scan(&status, &automatic)
	if e != nil || status != 2 || automatic {
		t.Fatal("hidden product auto-enabled", e)
	}
}
