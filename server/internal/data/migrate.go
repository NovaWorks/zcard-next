package data

// 运行时版本化迁移执行器（规划 §10.4）：启动时检测待执行迁移并加锁串行执行，
// 失败拒绝启动。迁移文件 go:embed 内嵌（migrations/<dialect>），生成走
// make migrate-diff（ent NamedDiff + dev-db），禁止手写 DDL。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"strings"
	"time"

	atlasmigrate "ariga.io/atlas/sql/migrate"
	atlasmysql "ariga.io/atlas/sql/mysql"
	atlaspostgres "ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/sqlclient"
	atlassqlite "ariga.io/atlas/sql/sqlite"

	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/migrations"
)

// revisionTable Atlas 迁移版本表（与 atlas CLI 互通）。
const revisionTable = "atlas_schema_revisions"

// ApplyMigrations 应用内嵌迁移：加锁 → 执行 pending 文件 → 记录版本。
// 幂等：已应用文件跳过（atlas_schema_revisions 版本比对）。
// dsn 用于 PG：经 sqlclient.OpenURL 打开（URL 注入 search_path 绑定 schema，
// 否则 atlas 的 CheckClean 会因 realm 含 public schema 误报 not clean）。
func ApplyMigrations(ctx context.Context, handle *sql.DB, d db.Dialect, dsn string) error {
	dirFS, err := migrations.FS(string(d))
	if err != nil {
		return err
	}
	// 无迁移文件（如全新方言线未生成）时静默跳过——生成流程见 Makefile
	if !hasMigrationFiles(dirFS) {
		return nil
	}

	unlock, err := lockMigrations(ctx, handle, d)
	if err != nil {
		return fmt.Errorf("migrate: 获取迁移锁失败: %w", err)
	}
	defer unlock()

	// PG 经 sqlclient.OpenURL 打开（atlas 需从 URL search_path 绑定 schema）；
	// MySQL/SQLite 直接复用主连接句柄
	var (
		drv     atlasmigrate.Driver
		revConn *sql.DB = handle
		closer  func()
	)
	if d == db.Postgres {
		u, err := url.Parse(dsn)
		if err != nil {
			return fmt.Errorf("migrate: 解析 PG DSN 失败: %w", err)
		}
		q := u.Query()
		if q.Get("search_path") == "" {
			q.Set("search_path", "public")
			u.RawQuery = q.Encode()
		}
		client, err := sqlclient.OpenURL(ctx, u)
		if err != nil {
			return fmt.Errorf("migrate: 打开 PG 连接失败: %w", err)
		}
		closer = func() { _ = client.Close() }
		drv = client
		revConn = client.DB
	} else {
		drv, err = atlasDriver(handle, d)
		if err != nil {
			return err
		}
	}
	defer func() {
		if closer != nil {
			closer()
		}
	}()
	rrw, err := newRevisionReadWriter(revConn, d)
	if err != nil {
		return err
	}
	dir, err := embedToMemDir(dirFS)
	if err != nil {
		return err
	}
	ex, err := atlasmigrate.NewExecutor(drv, dir, rrw)
	if err != nil {
		return err
	}
	// ExecuteN(ctx, 0)：应用全部 pending 迁移文件（记录版本到 atlas_schema_revisions）；
	// 全部已应用时返回 ErrNoPendingFiles，视为成功
	if err := ex.ExecuteN(ctx, 0); err != nil && !errors.Is(err, atlasmigrate.ErrNoPendingFiles) {
		return err
	}
	return nil
}

func hasMigrationFiles(fsys fs.FS) bool {
	found := false
	_ = fs.WalkDir(fsys, ".", func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !e.IsDir() && (strings.HasSuffix(e.Name(), ".sql") || e.Name() == "atlas.sum") {
			found = true
		}
		return nil
	})
	return found
}

// atlasDriver 由 *sql.DB 构造 atlas 迁移驱动（不经 sqlclient URL，直连已开句柄）。
func atlasDriver(handle *sql.DB, d db.Dialect) (atlasmigrate.Driver, error) {
	switch d {
	case db.MySQL:
		return atlasmysql.Open(handle)
	case db.Postgres:
		return atlaspostgres.Open(handle)
	case db.SQLite:
		return atlassqlite.Open(handle)
	}
	return nil, fmt.Errorf("migrate: 未知方言 %q", d)
}

// lockMigrations 启动迁移串行锁（§10.4）：MySQL GET_LOCK / PG pg_advisory_lock；
// SQLite 单写者天然串行（BEGIN IMMEDIATE），无需额外锁。
func lockMigrations(ctx context.Context, handle *sql.DB, d db.Dialect) (func(), error) {
	switch d {
	case db.MySQL:
		var got int
		if err := handle.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", d.LockName()).Scan(&got); err != nil || got != 1 {
			return nil, fmt.Errorf("migrate: MySQL GET_LOCK 未获取（got=%d err=%v）", got, err)
		}
		return func() {
			_, _ = handle.ExecContext(context.WithoutCancel(ctx), "SELECT RELEASE_LOCK(?)", d.LockName())
		}, nil
	case db.Postgres:
		if _, err := handle.ExecContext(ctx, "SELECT pg_advisory_lock(hashtext($1))", d.LockName()); err != nil {
			return nil, err
		}
		return func() {
			_, _ = handle.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock(hashtext($1))", d.LockName())
		}, nil
	}
	return func() {}, nil
}

// embedToMemDir 把内嵌迁移文件复制进 atlas MemDir（跳过 README 等非迁移文件）。
func embedToMemDir(fsys fs.FS) (*atlasmigrate.MemDir, error) {
	dir := &atlasmigrate.MemDir{}
	err := fs.WalkDir(fsys, ".", func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		name := path.Base(p)
		if name == "README.md" || strings.HasPrefix(name, ".") {
			return nil
		}
		content, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		return dir.WriteFile(name, content)
	})
	return dir, err
}

// revisionReadWriter DB 版本记录器（写 atlas_schema_revisions，与 atlas CLI 互通）。
type revisionReadWriter struct {
	handle  *sql.DB
	dialect db.Dialect
}

func newRevisionReadWriter(handle *sql.DB, d db.Dialect) (*revisionReadWriter, error) {
	// 跨方言通用 DDL：version 用 VARCHAR(191) 作主键（MySQL 不允许 TEXT 主键，
	// 与 atlas CLI 版本表口径一致；191 为 utf8mb4 索引安全长度）；operator_version
	// 用 varchar——MySQL 8 严格模式禁止 TEXT 列带字面量默认值（Error 1101/1170）
	const ddl = `CREATE TABLE IF NOT EXISTS ` + revisionTable + ` (
		version            VARCHAR(191) NOT NULL PRIMARY KEY,
		description        TEXT NOT NULL,
		type               INTEGER NOT NULL DEFAULT 1,
		applied            INTEGER NOT NULL DEFAULT 0,
		total              INTEGER NOT NULL,
		executed_at        TIMESTAMP NOT NULL,
		execution_time     BIGINT NOT NULL DEFAULT 0,
		error              TEXT,
		error_stmt         TEXT,
		hash               TEXT NOT NULL,
		partial_hashes     TEXT,
		operator_version   VARCHAR(64) NOT NULL DEFAULT ''
	)`
	if _, err := handle.Exec(ddl); err != nil {
		return nil, fmt.Errorf("migrate: 创建版本表失败: %w", err)
	}
	return &revisionReadWriter{handle: handle, dialect: d}, nil
}

// Ident 版本表标识。
func (w *revisionReadWriter) Ident() *atlasmigrate.TableIdent {
	return &atlasmigrate.TableIdent{Name: revisionTable}
}

// ph 第 i 个占位符（PG $i / MySQL、SQLite ?）。
func (w *revisionReadWriter) ph(i int) string {
	if w.dialect == db.Postgres {
		return fmt.Sprintf("$%d", i)
	}
	return "?"
}

// ReadRevisions 读取全部已应用版本。
func (w *revisionReadWriter) ReadRevisions(ctx context.Context) ([]*atlasmigrate.Revision, error) {
	rows, err := w.handle.QueryContext(ctx, `SELECT version, description, type, applied, total,
		executed_at, execution_time, error, error_stmt, hash, partial_hashes, operator_version
		FROM `+revisionTable+` ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var revs []*atlasmigrate.Revision
	for rows.Next() {
		r := &atlasmigrate.Revision{}
		var (
			execAt      time.Time
			execTime    int64
			errText     sql.NullString
			errStmt     sql.NullString
			partialHash sql.NullString
		)
		if err := rows.Scan(&r.Version, &r.Description, &r.Type, &r.Applied, &r.Total,
			&execAt, &execTime, &errText, &errStmt, &r.Hash, &partialHash, &r.OperatorVersion); err != nil {
			return nil, err
		}
		r.ExecutedAt = execAt
		r.ExecutionTime = time.Duration(execTime) * time.Millisecond
		r.Error = errText.String
		r.ErrorStmt = errStmt.String
		if partialHash.Valid && partialHash.String != "" {
			_ = json.Unmarshal([]byte(partialHash.String), &r.PartialHashes)
		}
		revs = append(revs, r)
	}
	return revs, rows.Err()
}

// ReadRevision 读单条。
func (w *revisionReadWriter) ReadRevision(ctx context.Context, version string) (*atlasmigrate.Revision, error) {
	revs, err := w.ReadRevisions(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range revs {
		if r.Version == version {
			return r, nil
		}
	}
	return nil, atlasmigrate.ErrRevisionNotExist
}

// WriteRevision 写版本记录。atlas executor 协议是 UPSERT 语义：
// 执行前先写入（标记 started）、每条语句后更新进度（partial hashes/applied 计数）、
// 完成或失败后终写（error/error_stmt）——纯 INSERT 会在第二条语句后撞唯一键。
func (w *revisionReadWriter) WriteRevision(ctx context.Context, r *atlasmigrate.Revision) error {
	cols := []string{"version", "description", "type", "applied", "total",
		"executed_at", "execution_time", "error", "error_stmt", "hash", "partial_hashes", "operator_version"}
	placeholders := make([]string, len(cols))
	for i := range cols {
		placeholders[i] = w.ph(i + 1)
	}
	partialJSON, err := json.Marshal(r.PartialHashes)
	if err != nil {
		return err
	}
	// 冲突时全量更新（进度推进/错误覆盖/完成态清理 partial）
	var conflict string
	switch w.dialect {
	case db.MySQL:
		conflict = ` ON DUPLICATE KEY UPDATE description=VALUES(description), type=VALUES(type),
			applied=VALUES(applied), total=VALUES(total), executed_at=VALUES(executed_at),
			execution_time=VALUES(execution_time), error=VALUES(error), error_stmt=VALUES(error_stmt),
			hash=VALUES(hash), partial_hashes=VALUES(partial_hashes), operator_version=VALUES(operator_version)`
	default: // PG / SQLite
		conflict = ` ON CONFLICT (version) DO UPDATE SET description=EXCLUDED.description, type=EXCLUDED.type,
			applied=EXCLUDED.applied, total=EXCLUDED.total, executed_at=EXCLUDED.executed_at,
			execution_time=EXCLUDED.execution_time, error=EXCLUDED.error, error_stmt=EXCLUDED.error_stmt,
			hash=EXCLUDED.hash, partial_hashes=EXCLUDED.partial_hashes, operator_version=EXCLUDED.operator_version`
	}
	_, err = w.handle.ExecContext(ctx,
		`INSERT INTO `+revisionTable+` (`+strings.Join(cols, ",")+`) VALUES (`+strings.Join(placeholders, ",")+`)`+conflict,
		r.Version, r.Description, r.Type, r.Applied, r.Total,
		r.ExecutedAt.UTC(), r.ExecutionTime.Milliseconds(), r.Error, r.ErrorStmt, r.Hash, string(partialJSON), r.OperatorVersion)
	return err
}

// DeleteRevision 删除版本记录。
func (w *revisionReadWriter) DeleteRevision(ctx context.Context, version string) error {
	_, err := w.handle.ExecContext(ctx,
		`DELETE FROM `+revisionTable+` WHERE version = `+w.ph(1), version)
	return err
}

var _ atlasmigrate.RevisionReadWriter = (*revisionReadWriter)(nil)
