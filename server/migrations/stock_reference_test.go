package migrations_test

import (
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestStockReferenceUpgradePreservesLegacyMappings(t *testing.T) {
	h, err := db.SQLite.Open(filepath.Join(t.TempDir(), "stock.db"))
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
		if strings.HasSuffix(f, "_stock_reference.sql") {
			upgrade = b
			break
		}
		if _, err = h.Exec(string(b)); err != nil {
			t.Fatal(f, err)
		}
	}
	if len(upgrade) == 0 {
		t.Fatal("migration missing")
	}
	for _, row := range []struct {
		code  string
		stock int
	}{{"valid", 37}, {"unknown", -2}, {"unlimited", -1}, {"zero", 0}} {
		if _, err = h.Exec(`INSERT INTO supply_mappings (created_at,updated_at,connection_id,upstream_product,local_product_id,up_stock,stock_checked_at,pricing_override) VALUES (CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,9,?,123,?,'2026-09-18 10:00:00','{"last_synced_price":1620}')`, row.code, row.stock); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = h.Exec(string(upgrade)); err != nil {
		t.Fatal(err)
	}
	rows, err := h.Query(`SELECT upstream_product,connection_id,local_product_id,up_stock,stock_reference,stock_reference_at,pricing_override FROM supply_mappings`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var name, price string
		var conn, id, stock, reference int
		var at any
		if err := rows.Scan(&name, &conn, &id, &stock, &reference, &at, &price); err != nil {
			t.Fatal(err)
		}
		count++
		if conn != 9 || id != 123 || reference != stock || !strings.Contains(price, "1620") {
			t.Fatal("mapping/price changed", name, stock, reference, price)
		}
		if (at == nil) != (stock == -2) {
			t.Fatal("invalid success time", name, at)
		}
	}
	if count != 4 {
		t.Fatal("rows lost", count)
	}
}
