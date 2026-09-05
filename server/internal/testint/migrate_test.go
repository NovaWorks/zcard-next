//go:build integration

// -2 全量迁移真跑（）：30+ 条三方言迁移 SQL 在空 MySQL/PG 真实执行——
// 终结「迁移只验证过 SQLite」。骨架 open() 已应用全量迁移，本文件补三段断言：
// 1. 版本表行数 == 方言迁移目录 .sql 文件数（一条不漏）
// 2. 幂等重跑：二次 ApplyMigrations 无错误无新增（ErrNoPendingFiles 路径）
// 3. 冲突拒绝：预置同名表 + 清空版本表 → 重放必须撞表报错
// （对应 启动语义「迁移失败拒绝启动」）
package testint

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

func TestMigrationsMySQL(t *testing.T) { runMigrations(MySQL(t)) }
func TestMigrationsPG(t *testing.T)    { runMigrations(PG(t)) }

func runMigrations(h *Harness) {
	t := h.T
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// 1) 版本表对账：一行一文件
	want := countMigrationFiles(t, string(h.D))
	got := revisionCount(h)
	if got != want {
		t.Fatalf("迁移版本表对账失败：%s 线 %d 行（目录 .sql 文件 %d 个）——有迁移未应用或版本表口径漂移", h.D, got, want)
	}
	t.Logf("迁移真跑对账通过：%s 线 %d 条全量应用", h.D, want)

	// 2) 幂等重跑（ErrNoPendingFiles → 成功且无新增）
	if err := data.ApplyMigrations(ctx, h.Data.DB, h.D, h.DSN); err != nil {
		t.Fatalf("幂等重跑失败: %v", err)
	}
	if got2 := revisionCount(h); got2 != want {
		t.Fatalf("幂等重跑后版本行数变化：%d → %d", want, got2)
	}
}

// TestMigrationsConflictMySQL/PG 冲突拒绝路径（空 schema + 脏表 → 迁移必须报错）。
func TestMigrationsConflictMySQL(t *testing.T) { runConflict(MySQLNoMigrate(t)) }
func TestMigrationsConflictPG(t *testing.T)    { runConflict(PGNoMigrate(t)) }

func runConflict(h *Harness) {
	t := h.T
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// 空 schema 预置脏表（products 必在首个建表迁移清单内）→ 全量迁移
	// 应撞表报错；不报错 = 冲突检测失守 = 迁移管线失守
	if _, err := h.Data.DB.ExecContext(ctx, "CREATE TABLE products (id BIGINT PRIMARY KEY)"); err != nil {
		t.Fatalf("预置冲突表失败: %v", err)
	}
	if err := data.ApplyMigrations(ctx, h.Data.DB, h.D, h.DSN); err == nil {
		t.Fatal("冲突迁移必须失败（预置同名表 → 首个建表迁移应撞表），却返回成功")
	}
	t.Logf("冲突拒绝路径通过（预期撞表报错）")
}

// ── 工具 ────────────────────────────────────────────────────────

// revisionCount 版本表行数（连接已绑定隔离库/search_path）。
func revisionCount(h *Harness) int {
	t := h.T
	var n int
	if err := h.Data.DB.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM atlas_schema_revisions").Scan(&n); err != nil {
		t.Fatalf("读版本表失败: %v", err)
	}
	return n
}

func countMigrationFiles(t *testing.T, dialect string) int {
	t.Helper()
	fsys, err := migrations.FS(dialect)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	err = fs.WalkDir(fsys, ".", func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}
