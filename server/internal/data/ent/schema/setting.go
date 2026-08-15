package schema

// 所有权：mods/settings（M0）

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"encoding/json"
)

// Setting 运行时业务开关的真理源（铁律 7：后台可改；env/config 只作首次部署兜底）。
type Setting struct {
	ent.Schema
}

func (Setting) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Setting) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		// group 为 SQL 保留字，Ent/Atlas 生成 DDL 时会引用标识符
		field.String("group").MaxLen(50).Comment("设置分组：site/template/trade/security/ops/notify..."),
		field.String("key").MaxLen(100),
		// value 为任意 JSON 文档（对象/数组/标量），json.RawMessage 保持原样存储
		field.JSON("value", json.RawMessage{}).Comment("设置值（JSON 文档）"),
	}
}

func (Setting) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group", "key").Unique(),
	}
}
