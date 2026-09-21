package migrations_test

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

func TestBatchContentSQLiteUpgradePreservesProducts(t *testing.T) {
	handle, err := db.SQLite.Open(filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	handle.SetMaxOpenConns(1)
	dir, _ := migrations.FS("sqlite")
	files, _ := fs.Glob(dir, "*.sql")
	var upgrade []byte
	for _, name := range files {
		sql, err := fs.ReadFile(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, "_product_batch_content.sql") {
			upgrade = sql
			break
		}
		if _, err = handle.Exec(string(sql)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if len(upgrade) == 0 {
		t.Fatal("migration missing")
	}
	if strings.Contains(string(upgrade), "DROP TABLE") {
		t.Fatal("upgrade must be additive")
	}
	_, err = handle.Exec(`INSERT INTO products(id,created_at,updated_at,name,slug,price,description,cover,images,direct_content) VALUES (42,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'keep','keep',999,'original','/uploads/old.png','["/uploads/gallery.png"]',X'1234')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = handle.Exec(string(upgrade)); err != nil {
		t.Fatal(err)
	}
	var name, desc, cover, images string
	var price int
	var coverProtected, descProtected bool
	var content []byte
	err = handle.QueryRow(`SELECT name,description,cover,images,price,direct_content,cover_protected,description_protected FROM products WHERE id=42`).Scan(&name, &desc, &cover, &images, &price, &content, &coverProtected, &descProtected)
	if err != nil {
		t.Fatal(err)
	}
	if name != "keep" || desc != "original" || cover != "/uploads/old.png" || images != `["/uploads/gallery.png"]` || price != 999 || len(content) != 2 || coverProtected || descProtected {
		t.Fatal("upgrade changed existing product")
	}
}
