package migratev1

// 源库（1.x MySQL，只读）连接与巡查询。

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql" // 源方言固定 MySQL
)

// Source 1.x 源库句柄。约定只读：迁移全程仅 SELECT，建议配只读账号。
type Source struct {
	DB *sql.DB
}

// OpenSource 打开源库（dsn 为 go-sql-driver 格式；由 --dsn-old 或 --old-env 构造）。
func OpenSource(dsn string) (*Source, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 1.x 源库失败: %w", err)
	}
	// 巡检/分页读无需高并发；限制连接数降低对生产源库的冲击
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	return &Source{DB: db}, nil
}

// Close 关闭源库。
func (s *Source) Close() error { return s.DB.Close() }

// ServerVersion 源库版本（巡检报告展示）。
func (s *Source) ServerVersion(ctx context.Context) (string, error) {
	var v string
	if err := s.DB.QueryRowContext(ctx, "SELECT VERSION()").Scan(&v); err != nil {
		return "", err
	}
	return v, nil
}

// TableExists 表是否存在。
func (s *Source) TableExists(ctx context.Context, table string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?", table).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ColumnDataType 列的 DATA_TYPE（information_schema；表/列不存在返回错误）。
func (s *Source) ColumnDataType(ctx context.Context, table, column string) (string, error) {
	var dt string
	err := s.DB.QueryRowContext(ctx,
		"SELECT DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?",
		table, column).Scan(&dt)
	if err != nil {
		return "", fmt.Errorf("探测列 %s.%s 失败: %w", table, column, err)
	}
	return dt, nil
}

// CountRows 表行数（巡检规模概览；大表 COUNT 可能偏慢，仅在 preflight 使用）。
func (s *Source) CountRows(ctx context.Context, table string) (int64, error) {
	var n sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"`").Scan(&n); err != nil {
		return 0, err
	}
	return n.Int64, nil
}

// SampleStrings 取单列前 limit 行（密钥抽样自检用；NULL 跳过）。
func (s *Source) SampleStrings(ctx context.Context, table, column string, limit int) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		fmt.Sprintf("SELECT `%s` FROM `%s` WHERE `%s` IS NOT NULL ORDER BY id LIMIT %d", column, table, column, limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SettingValue 读 settings 表单个 key 的 value（JSON 编码字符串；不存在返回空串与 false）。
func (s *Source) SettingValue(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE `key` = ?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}
