package migrations_test

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

func TestCheckoutSQLiteUpgradePreservesOrders(t *testing.T) {
	h, err := db.SQLite.Open(filepath.Join(t.TempDir(), "checkout.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.SetMaxOpenConns(1)
	dir, _ := migrations.FS("sqlite")
	files, _ := fs.Glob(dir, "*.sql")
	var upgrade []byte
	for _, f := range files {
		b, err := fs.ReadFile(dir, f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(f, "_checkout_expiry_stock.sql") {
			upgrade = b
			break
		}
		if _, err = h.Exec(string(b)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if len(upgrade) == 0 {
		t.Fatal("migration missing")
	}
	if _, err = h.Exec(`INSERT INTO orders (id,created_at,updated_at,order_no,total_amount,expired_at) VALUES (1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'keep-order',760,'2026-09-09 12:30:00')`); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Exec(`INSERT INTO payments (id,created_at,updated_at,order_id,channel,amount) VALUES (1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,1,'epay',760)`); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Exec(string(upgrade)); err != nil {
		t.Fatal(err)
	}
	var amount, review, attempts, channelID int
	if err = h.QueryRow(`SELECT total_amount,expiry_review,expiry_attempts FROM orders WHERE order_no='keep-order' AND expired_at='2026-09-09 12:30:00'`).Scan(&amount, &review, &attempts); err != nil {
		t.Fatal(err)
	}
	if amount != 760 || review != 0 || attempts != 0 {
		t.Fatal("historical order changed")
	}
	if err = h.QueryRow(`SELECT channel_id FROM payments WHERE order_id=1 AND channel='epay' AND amount=760`).Scan(&channelID); err != nil || channelID != 0 {
		t.Fatalf("historical payment changed: %v", err)
	}
	rows, err := h.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migration broke foreign keys")
	}
}
