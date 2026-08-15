package db

import (
	"fmt"
	"strings"
)

// SQL 跨方言 SQL 原语构造器——internal/data/report 层唯一允许的裸 SQL 构造入口。
// 业务代码一律走 Ent builder；在业务代码拼接本包产出的方言 SQL 同样被架构测试拦截。
type SQL struct{ d Dialect }

// New 按方言构造。
func New(d Dialect) *SQL { return &SQL{d: d} }

// Dialect 返回当前方言。
func (s *SQL) Dialect() Dialect { return s.d }

// Placeholder 第 i 个占位符（从 1 起）：MySQL/SQLite `?`，PG `$i`。
func (s *SQL) Placeholder(i int) string {
	if s.d == Postgres {
		return fmt.Sprintf("$%d", i)
	}
	return "?"
}

// Placeholders 生成 n 个占位符（逗号分隔），用于 IN (...)。
func (s *SQL) Placeholders(n, from int) string {
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = s.Placeholder(from + i)
	}
	return strings.Join(parts, ", ")
}

// QuoteIdent 引用标识符：MySQL “ `id` “，PG/SQLite `"id"`。
func (s *SQL) QuoteIdent(id string) string {
	if s.d == MySQL {
		return "`" + id + "`"
	}
	return `"` + id + `"`
}

// ILike 大小写不敏感匹配表达式（片段内含占位符）。
func (s *SQL) ILike(col string) string {
	if s.d == Postgres {
		return col + " ILIKE ?"
	}
	return "LOWER(" + col + ") LIKE LOWER(?)"
}

// Concat 字符串拼接表达式。
func (s *SQL) Concat(exprs ...string) string {
	if s.d == MySQL {
		return "CONCAT(" + strings.Join(exprs, ", ") + ")"
	}
	return "(" + strings.Join(exprs, " || ") + ")"
}

// DateAdd 日期加减表达式：unit 取 second/minute/hour/day。
func (s *SQL) DateAdd(col, unit string, n int) string {
	switch s.d {
	case MySQL:
		return fmt.Sprintf("DATE_ADD(%s, INTERVAL %d %s)", col, n, strings.ToUpper(unit))
	case Postgres:
		return fmt.Sprintf("(%s + (%d * interval '1 %s'))", col, n, unit)
	default: // SQLite
		sign := "+"
		if n < 0 {
			sign, n = "-", -n
		}
		return fmt.Sprintf("datetime(%s, '%s%d %s')", col, sign, n, unit)
	}
}

// Bool 布尔字面量：MySQL 1/0，PG/SQLite TRUE/FALSE。
func (s *SQL) Bool(v bool) string {
	if s.d == MySQL {
		if v {
			return "1"
		}
		return "0"
	}
	if v {
		return "TRUE"
	}
	return "FALSE"
}

// Paginate 分页子句：MySQL `LIMIT offset, limit`，PG/SQLite `LIMIT limit OFFSET offset`。
func (s *SQL) Paginate(limit, offset int) string {
	if s.d == MySQL {
		return fmt.Sprintf("LIMIT %d, %d", offset, limit)
	}
	return fmt.Sprintf("LIMIT %d OFFSET %d", limit, offset)
}

// ForUpdate 行锁子句（下单锁卡/支付回调热路径）。
// SQLite 返回空串：无行锁，走 BEGIN IMMEDIATE 单写者 + affected rows CAS 语义（§5.20.3）。
func (s *SQL) ForUpdate(skipLocked bool) string {
	switch s.d {
	case MySQL, Postgres:
		if skipLocked {
			return "FOR UPDATE SKIP LOCKED"
		}
		return "FOR UPDATE"
	}
	return ""
}

// NowSQL 当前时间表达式。
func (s *SQL) NowSQL() string {
	if s.d == SQLite {
		return "CURRENT_TIMESTAMP"
	}
	return "NOW()"
}
