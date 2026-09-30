package migrations_test

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

func TestFetchFailureCountUpgradePreservesLocks(t *testing.T) {
	h, err := db.SQLite.Open(filepath.Join(t.TempDir(), "locks.db"))
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
		if strings.HasSuffix(f, "_fetch_failure_count.sql") {
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
	if _, err = h.Exec(`INSERT INTO risk_lock_keys(key_hash,expires_at,created_at) VALUES('legacy','2030-01-01 00:00:00','2026-09-30 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Exec(string(upgrade)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = h.QueryRow(`SELECT failure_count FROM risk_lock_keys WHERE key_hash='legacy' AND expires_at='2030-01-01 00:00:00'`).Scan(&count); err != nil || count != 5 {
		t.Fatal("legacy lock changed", count, err)
	}
	if _, err = h.Exec(`INSERT INTO risk_lock_keys(key_hash,failure_count,expires_at,created_at) VALUES('new',1,'2030-01-01 00:00:00',CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err = h.QueryRow(`SELECT failure_count FROM risk_lock_keys WHERE key_hash='new'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
