package migrations_test

import (
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlashPaidUpgradePreservesHistoricalCounters(t *testing.T) {
	h, err := db.SQLite.Open(filepath.Join(t.TempDir(), "flash.db"))
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
		if strings.HasSuffix(f, "_flash_paid_reservation.sql") {
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
	_, err = h.Exec(`INSERT INTO flash_sales (id,created_at,updated_at,product_id,sku_id,flash_price,start_at,end_at,limit_qty,sold_qty,per_user_limit) VALUES (1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,1,0,100,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,20,7,2)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Exec(string(upgrade)); err != nil {
		t.Fatal(err)
	}
	var sold, reserved int
	if err = h.QueryRow("SELECT sold_qty,reserved_qty FROM flash_sales WHERE id=1").Scan(&sold, &reserved); err != nil || sold != 7 || reserved != 0 {
		t.Fatal("history changed", sold, reserved, err)
	}
}
