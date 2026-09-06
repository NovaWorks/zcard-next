package migratev1

// 通用迁移引擎：keyset 分批扫描源表 → 每批一个目标事务（行转换 + v1id_maps 原子写入）。
// 幂等语义：行级——old_id 已在 v1id_maps 即跳过（重跑=增量，切换窗口模型的基础）。
// 源端经 *sql.DB 抽象（MySQL 生产 / sqlite 单测双跑），扫描 SQL 保持双方言兼容。

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
)

// Migrator 一次迁移会话（preflight 通过后构造）。
type Migrator struct {
	Src    *Source
	Client *ent.Client
	IDs    *IDMapper
	RW     *ReportWriter
	Opts   Options

	// 1.x 密钥（preflight 解析产物）
	AppKey, CardKey []byte
	// 2.0 新密钥（凭据重加密 DataBox / 卡密 CardCipher 用）
	DataKey []byte

	TZ  *time.Location
	st  *Stats
	dry bool
}

// NewMigrator 构造。
func NewMigrator(src *Source, client *ent.Client, ids *IDMapper, rw *ReportWriter, opts Options, tz string) *Migrator {
	if opts.Batch <= 0 {
		opts.Batch = 1000
	}
	return &Migrator{
		Src:    src,
		Client: client,
		IDs:    ids,
		RW:     rw,
		Opts:   opts,
		TZ:     srcTimezone(tz),
		dry:    opts.DryRun,
		st:     NewStats(),
	}
}

// Stats 迁移统计（报告用）。
func (m *Migrator) Stats() *Stats { return m.st }

// Stats 逐表计数。
type Stats struct {
	Tables map[string]*TableStat
}

// TableStat 单表统计。Planned 仅 dry-run 填充（源行数预估）。
type TableStat struct {
	Migrated      int64 `json:"migrated"`
	SkippedExists int64 `json:"skipped_exists"` // 幂等跳过（已在 v1id_maps / 唯一键存在）
	Failed        int64 `json:"failed"`
	Planned       int64 `json:"planned,omitempty"`
}

// NewStats 构造。
func NewStats() *Stats { return &Stats{Tables: map[string]*TableStat{}} }

func (s *Stats) table(name string) *TableStat {
	t, ok := s.Tables[name]
	if !ok {
		t = &TableStat{}
		s.Tables[name] = t
	}
	return t
}

// Record 记一笔。
func (s *Stats) Record(table string, outcome string) {
	t := s.table(table)
	switch outcome {
	case "migrated":
		t.Migrated++
	case "skip":
		t.SkippedExists++
	case "fail":
		t.Failed++
	}
}

// scanTable keyset 分批扫描。dest 每行调用一次返回 scan 目标切片（首列必须是 id *int64）；
// fn 每行调用一次做转换写入（返回 error 计入失败行）。dry 模式只计数不调 fn。
func (m *Migrator) scanTable(ctx context.Context, table string, cols []string,
	dest func() []any, fn func(id int64) error) error {

	t := m.st.table(table)
	if m.dry {
		var n int64
		if err := m.Src.DB.QueryRowContext(ctx,
			fmt.Sprintf("SELECT COUNT(*) FROM `%s`", table)).Scan(&n); err != nil {
			return err
		}
		t.Migrated = 0
		t.SkippedExists = 0
		t.Failed = 0
		t.Planned = n
		return nil
	}

	colList := make([]string, len(cols))
	for i, c := range cols {
		colList[i] = "`" + c + "`"
	}
	query := fmt.Sprintf("SELECT %s FROM `%s` WHERE `id` > ? ORDER BY `id` LIMIT ?",
		strings.Join(colList, ", "), table)

	var cursor int64
	for {
		rows, err := m.Src.DB.QueryContext(ctx, query, cursor, m.Opts.Batch)
		if err != nil {
			return fmt.Errorf("扫描 %s 失败: %w", table, err)
		}
		var batchLast int64
		count := 0
		for rows.Next() {
			targets := dest()
			if err := rows.Scan(targets...); err != nil {
				rows.Close()
				return fmt.Errorf("扫描 %s 行失败: %w", table, err)
			}
			id := *targets[0].(*int64)
			batchLast = id
			count++
			if err := fn(id); err != nil {
				if m.Opts.OnError == "abort" {
					rows.Close()
					return fmt.Errorf("表 %s 行 %d 迁移失败（abort）: %w", table, id, err)
				}
				t.Failed++
				m.RW.AddError(table, uint64(id), err.Error())
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if count == 0 {
			return nil
		}
		cursor = batchLast
		if count < m.Opts.Batch {
			return nil
		}
	}
}

// ErrNotDelivered 未交付阶段的占位错误。
var ErrNotDelivered = fmt.Errorf("该阶段尚未交付（按计划 P2 起逐阶段实现）")

// nullStr sql.NullString 取值（NULL → 空串；时间列 NULL 即空串语义）。
func nullStr(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}

// nullInt sql.NullInt64 取值（NULL → 0）。
func nullInt(v sql.NullInt64) int64 {
	if v.Valid {
		return v.Int64
	}
	return 0
}
