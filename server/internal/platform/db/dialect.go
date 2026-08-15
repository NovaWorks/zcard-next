// Package db 是全仓唯一允许触碰数据库方言的包（ADR-D20）。
//
// 三个方向钉死（规划 §3.7）：
//  1. 运行时能力开关（Detect），不是 build tag——同一二进制三方言；
//  2. 能力降级是显式分支（Capabilities），禁止「假装支持然后运行时炸 SQL」；
//  3. report 层裸 SQL 必须经 SQL 原语构造；mods/* 禁止 import 驱动特定包（架构测试强制）。
//
// 业务代码 95% 走 Ent builder 天然跨方言；本包只服务：
//   - 启动装配（按 DSN 打开对应驱动的 *sql.DB）；
//   - internal/data/report 的跨方言裸 SQL 构造；
//   - 方言能力判定（如 SQLite 无 FOR UPDATE 时走 BEGIN IMMEDIATE + CAS）。
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Dialect 数据库方言。
type Dialect string

const (
	MySQL    Dialect = "mysql"
	Postgres Dialect = "postgres"
	SQLite   Dialect = "sqlite"
)

// Detect 从配置的 driver 名检测方言（config.yaml data.database.driver）。
// 兼容常见别名（mariadb/postgresql/sqlite3）。
func Detect(driver string) (Dialect, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "mysql", "mariadb":
		return MySQL, nil
	case "postgres", "postgresql", "pgx":
		return Postgres, nil
	case "sqlite", "sqlite3":
		return SQLite, nil
	}
	return "", fmt.Errorf("db: unsupported driver %q (supported: sqlite | mysql | postgres)", driver)
}

// DriverName 返回 database/sql 注册的驱动名。
// SQLite 使用纯 Go 驱动 modernc.org/sqlite（无 CGO，保单二进制交叉编译，ADR-D19）。
func (d Dialect) DriverName() string {
	switch d {
	case MySQL:
		return "mysql"
	case Postgres:
		return "pgx" // jackc/pgx/v5/stdlib
	case SQLite:
		return "sqlite" // modernc.org/sqlite
	}
	return string(d)
}

// Open 按 DSN 打开连接池（统一入口：驱动选择收口在本包，mods 禁 import 驱动包）。
// SQLite 自动启用 WAL + busy_timeout（ADR-D19 技术要点 2），并确保父目录存在；
// MySQL 自动补 parseTime/loc（驱动返回 time.Time 的必要参数，漏配是常见接入错误）。
func (d Dialect) Open(dsn string) (*sql.DB, error) {
	switch d {
	case SQLite:
		dsn = ensureSQLitePragmas(dsn)
		if err := ensureSQLiteDir(dsn); err != nil {
			return nil, err
		}
	case MySQL:
		dsn = ensureMySQLParams(dsn)
	}
	handle, err := sql.Open(d.DriverName(), dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", d, err)
	}
	return handle, nil
}

// ensureMySQLParams 补齐驱动必需参数：parseTime=True（扫描 time 列）与 loc=UTC
// （统一 UTC，§3.4）；用户已配置则不覆盖（如自定义 loc 的多租户场景）。
func ensureMySQLParams(dsn string) string {
	// DSN 形如 user:pass@tcp(host:port)/db?params
	if !strings.Contains(dsn, "parseTime=") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + "parseTime=True&loc=UTC"
	}
	return dsn
}

func ensureSQLitePragmas(dsn string) string {
	if strings.Contains(dsn, "_pragma") {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
}

// ensureSQLiteDir SQLite 文件父目录自动创建（file:data/zcard.db → mkdir data/）。
func ensureSQLiteDir(dsn string) error {
	path := strings.TrimPrefix(dsn, "file:")
	if i := strings.IndexAny(path, "?"); i >= 0 {
		path = path[:i]
	}
	if path == "" || path == ":memory:" {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("db: 创建 SQLite 目录 %s 失败: %w", dir, err)
	}
	return nil
}

// LockName 返回 Dialect 对应的迁移锁名（启动迁移串行化，规划 §10.4）。
func (d Dialect) LockName() string { return "zcard_migrate" }
