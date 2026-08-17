//go:build integration

// Package testint CI 集成测试骨架（P0-05，主文档 §13 方言三线矩阵的 MySQL/PG 线）。
//
// 语义：
//   - 仅 `-tags=integration` 编译（make test 单元线零影响）；
//   - DSN 走环境变量，未配置自动 Skip（本地无 Docker 不污染）：
//     ZCARD_TEST_MYSQL_DSN  管理连接（指向任意可写库，如 /mysql）
//     ZCARD_TEST_PG_DSN     管理连接（URL 形式，指向任意可写库，如 /postgres）
//   - 每次运行创建隔离目标（MySQL CREATE DATABASE / PG CREATE SCHEMA，
//     名字含测试名 + 纳秒后缀），defer DROP——并行与重跑互不踩踏；
//   - 骨架内即跑全量真迁移（data.ApplyMigrations）——每个集成用例
//     天然建立在「迁移后的真实 schema」之上，迁移正确性随用例持续回归。
package testint

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
)

// DSN 环境变量。
const (
	EnvMySQL = "ZCARD_TEST_MYSQL_DSN"
	EnvPG    = "ZCARD_TEST_PG_DSN"
)

// Harness 一次集成测试的隔离环境（真库 + 真迁移 + 独立库/schema）。
type Harness struct {
	T    *testing.T
	Data *data.Data
	DSN  string // 目标 DSN（已含库名 / search_path，可直接喂给 data.NewData）
	D    db.Dialect

	admin    *sql.DB // 管理连接（建/删隔离目标）
	adminDSN string
	target   string // 隔离库名（MySQL）/ schema 名（PG）
}

// MySQL 打开 MySQL 集成环境（EnvMySQL 未配置 → Skip）。
// 环境就绪后自动应用全量迁移（用例建立在真实迁移 schema 之上）。
func MySQL(t *testing.T) *Harness {
	t.Helper()
	return open(t, db.MySQL, envOrSkip(t, EnvMySQL, "root:root@tcp(127.0.0.1:3306)/mysql"), true)
}

// PG 打开 PostgreSQL 集成环境（EnvPG 未配置 → Skip）。语义同 MySQL。
func PG(t *testing.T) *Harness {
	t.Helper()
	return open(t, db.Postgres, envOrSkip(t, EnvPG, "postgres://postgres:pass@127.0.0.1:5432/postgres?sslmode=disable"), true)
}

// MySQLNoMigrate / PGNoMigrate 打开隔离环境但不跑迁移（空 schema）——
// 迁移用例自身驱动 ApplyMigrations 时使用（如冲突拒绝路径）。
func MySQLNoMigrate(t *testing.T) *Harness {
	t.Helper()
	return open(t, db.MySQL, envOrSkip(t, EnvMySQL, ""), false)
}

func PGNoMigrate(t *testing.T) *Harness {
	t.Helper()
	return open(t, db.Postgres, envOrSkip(t, EnvPG, ""), false)
}

func envOrSkip(t *testing.T, env, example string) string {
	t.Helper()
	v := osGetenv(env)
	if v == "" {
		t.Skipf("集成线：环境变量 %s 未配置（如 %s）", env, example)
	}
	return v
}

func open(t *testing.T, d db.Dialect, adminDSN string, migrate bool) *Harness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	target := isoName(t.Name())
	h := &Harness{T: t, D: d, adminDSN: adminDSN, target: target}

	// 1) 管理连接：建隔离目标
	admin, err := d.Open(adminDSN)
	if err != nil {
		t.Fatalf("集成线：打开管理连接失败: %v", err)
	}
	h.admin = admin
	switch d {
	case db.MySQL:
		if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+target+" CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
			_ = admin.Close()
			t.Fatalf("集成线：建库失败: %v", err)
		}
		h.DSN = mysqlWithDB(adminDSN, target)
	case db.Postgres:
		u, err := url.Parse(adminDSN)
		if err != nil {
			_ = admin.Close()
			t.Fatalf("集成线：PG DSN 非法: %v", err)
		}
		if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+target); err != nil {
			_ = admin.Close()
			t.Fatalf("集成线：建 schema 失败: %v", err)
		}
		q := u.Query()
		q.Set("search_path", target)
		u.RawQuery = q.Encode()
		h.DSN = u.String()
	}

	// 2) 目标连接：池上限固定 50（生产拓扑：共享池多连接并发事务；
	//    同时防 PG max_connections=100 默认值被打爆——50 路真锁竞争已远超
	//    SQLite 单写者串行路径的覆盖面）
	dh, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{
		Driver: string(d), Source: h.DSN,
		MaxOpenConns: 50, MaxIdleConns: 50,
	}})
	if err != nil {
		h.drop()
		t.Fatalf("集成线：打开目标连接失败: %v", err)
	}
	if migrate {
		if err := data.ApplyMigrations(ctx, dh.DB, d, h.DSN); err != nil {
			cleanup()
			h.drop()
			t.Fatalf("集成线：全量迁移失败（方言 %s——迁移 SQL 真跑即验收）: %v", d, err)
		}
	}
	h.Data = dh

	t.Cleanup(func() {
		cleanup()
		h.drop()
	})
	return h
}

// drop 删除隔离目标（库/schema）并关管理连接。
func (h *Harness) drop() {
	if h.admin == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch h.D {
	case db.MySQL:
		_, _ = h.admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+h.target)
	case db.Postgres:
		_, _ = h.admin.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+h.target+" CASCADE")
	}
	_ = h.admin.Close()
	h.admin = nil
}

// isoName 测试名 → 合法标识符（小写、[a-z0-9_]、长度上限 40 + 纳秒后缀）。
func isoName(testName string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(path.Base(testName)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
		if b.Len() >= 40 {
			break
		}
	}
	return fmt.Sprintf("zcard_it_%s_%d", b.String(), time.Now().UnixNano()%1_000_000)
}

// mysqlWithDB 替换 MySQL DSN 的库名段（[user@tcp(addr)/]db[?params]）。
func mysqlWithDB(dsn, dbname string) string {
	query := ""
	if i := strings.Index(dsn, "?"); i >= 0 {
		query = dsn[i:]
		dsn = dsn[:i]
	}
	i := strings.LastIndex(dsn, "/")
	if i < 0 {
		return dsn + "/" + dbname + query
	}
	return dsn[:i+1] + dbname + query
}

func osGetenv(k string) string { return strings.TrimSpace(os.Getenv(k)) }
