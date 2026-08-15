package db

import "fmt"

// Capabilities 方言能力开关（运行时判定，非编译期）。
// 业务代码据以下降级——显式分支，不是运行时碰运气（降级铁律，ADR-D20）。
type Capabilities struct {
	SupportsRLS          bool // 行级安全策略（仅 PG；MySQL/SQLite 靠 Ent 拦截器单层保障）
	SupportsSchema       bool // 独立 schema 命名空间（PG 原生；MySQL database==schema；SQLite attach 模拟默认不用）
	SupportsJSONB        bool // JSONB 高级查询/GIN 索引（PG；MySQL JSON 弱等价；SQLite JSON1 弱）
	SupportsReturning    bool // INSERT/UPDATE ... RETURNING（PG/SQLite；MySQL 无）
	SupportsILIKE        bool // ILIKE（PG；MySQL/SQLite 用 LOWER 降级）
	SupportsForUpdate    bool // SELECT ... FOR UPDATE（PG/MySQL；SQLite 无，走 BEGIN IMMEDIATE + CAS）
	SupportsSkipLocked   bool // FOR UPDATE SKIP LOCKED（PG/MySQL8；高并发锁卡利器）
	SupportsArray        bool // 数组列（PG；MySQL/SQLite 用 JSON 模拟）
	SupportsPartialIndex bool // 部分索引（PG/SQLite；MySQL 无）
	SupportsWindowFunc   bool // 窗口函数（三方言均支持，SQLite 需 3.25+）
}

// Capabilities 返回该方言的能力开关。
func (d Dialect) Capabilities() Capabilities {
	switch d {
	case Postgres:
		return Capabilities{
			SupportsRLS: true, SupportsSchema: true, SupportsJSONB: true,
			SupportsReturning: true, SupportsILIKE: true, SupportsForUpdate: true,
			SupportsSkipLocked: true, SupportsArray: true, SupportsPartialIndex: true,
			SupportsWindowFunc: true,
		}
	case MySQL:
		return Capabilities{
			SupportsForUpdate:  true,
			SupportsSkipLocked: true,
			SupportsWindowFunc: true,
		}
	case SQLite:
		return Capabilities{
			// SQLite 无行锁：单写者 + BEGIN IMMEDIATE 串行化 + UPDATE...WHERE status=CAS（§5.20.3）
			SupportsReturning:    true,
			SupportsPartialIndex: true,
			SupportsWindowFunc:   true,
		}
	}
	return Capabilities{}
}

// ErrUnsupported 能力不支持时返回的显式错误（禁止静默生成非法 SQL）。
type ErrUnsupported struct {
	Dialect Dialect
	Cap     string
}

func (e *ErrUnsupported) Error() string {
	return fmt.Sprintf("db: %s does not support %s（显式降级分支，不是静默失败）", e.Dialect, e.Cap)
}

// Require 断言能力可用，不可用返回 *ErrUnsupported（调用方走降级路径或明确报错）。
func (d Dialect) Require(cap string, ok bool) error {
	if !ok {
		return &ErrUnsupported{Dialect: d, Cap: cap}
	}
	return nil
}
