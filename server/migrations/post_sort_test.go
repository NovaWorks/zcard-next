package migrations_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

func TestPostSortSQLiteUpgrade(t *testing.T) {
	handle, err := db.SQLite.Open(filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	handle.SetMaxOpenConns(1)
	dir, err := migrations.FS("sqlite")
	if err != nil {
		t.Fatal(err)
	}
	files, err := fs.Glob(dir, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	var upgrade []byte
	for _, file := range files {
		sql, err := fs.ReadFile(dir, file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(file, "_post_sort.sql") {
			upgrade = sql
			break
		}
		if _, err := handle.Exec(string(sql)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	if len(upgrade) == 0 {
		t.Fatal("post sort migration missing")
	}
	_, err = handle.Exec(`INSERT INTO posts (id, created_at, updated_at, slug, title_json, content_json, is_published) VALUES (42, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'keep-me', '{"zh_CN":"原文章"}', '{"zh_CN":"原内容"}', true)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Exec(string(upgrade)); err != nil {
		t.Fatal(err)
	}
	var title, content string
	var sort int
	if err := handle.QueryRow(`SELECT title_json, content_json, sort FROM posts WHERE id=42 AND slug='keep-me'`).Scan(&title, &content, &sort); err != nil {
		t.Fatal(err)
	}
	if sort != 0 || !strings.Contains(title, "原文章") || !strings.Contains(content, "原内容") {
		t.Fatalf("article changed: %s %s %d", title, content, sort)
	}
	if _, err := handle.Exec(`UPDATE posts SET sort=5 WHERE id=42`); err != nil {
		t.Fatal(err)
	}
}

func TestPostSortSQLiteMigrationReplay(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "replay.db")
	handle, err := db.SQLite.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	for i := 0; i < 2; i++ {
		if err := data.ApplyMigrations(context.Background(), handle, db.SQLite, dsn); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := handle.Exec(`INSERT INTO posts (created_at, updated_at, slug, title_json, content_json, sort) VALUES (CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'new', '{}', '{}', 3)`); err != nil {
		t.Fatal(err)
	}
}
