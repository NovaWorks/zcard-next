package schema

// mysqlTime 时间字段的 MySQL 方言列类型（全 schema 统一引用）。
// 《数据库架构设计.md》§0 类型映射钉死 MySQL `DATETIME(3)`（毫秒精度，无 2038 上限）；
// ent 对 MySQL 的默认 time 类型是 `timestamp`（秒精度 + 2038 上限），与文档偏差。
// PG（timestamptz）与 SQLite（ISO8601 文本）由驱动默认产出正确类型。
//
// 规则（由 schema_test.go 强制）：新增 time 字段一律写成
//	field.Time("xxx").SchemaType(mysqlTime)
import "entgo.io/ent/dialect"

var mysqlTime = map[string]string{
	dialect.MySQL: "datetime(3)",
}
