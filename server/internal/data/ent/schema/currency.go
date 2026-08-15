package schema

// 所有权：platform（多币种展示，规划 §5.1；记账永远走 platform/money.Cents）

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Currency 展示货币配置（1.x CurrencyService 表结构平移）。
// 记账只认基础货币（默认 CNY）；汇率仅用于展示换算与下单快照。
type Currency struct {
	ent.Schema
}

func (Currency) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Currency) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("code").MaxLen(3).Unique().Comment("ISO 4217 三字码"),
		field.String("symbol").MaxLen(10).Comment("货币符号（¥/$）"),
		field.Enum("position").Values("prefix", "suffix").Default("prefix").Comment("符号位置"),
		field.Int32("precision").Default(2).Comment("小数位"),
		// 汇率不是金额（铁律 1 只约束金额列），存储用 decimal/numeric 精确类型
		field.Float("rate").
			Default(1).
			SchemaType(map[string]string{
				dialect.MySQL:     "decimal(20,8)",
				dialect.Postgres:  "numeric(20,8)",
				dialect.SQLite:    "real",
			}).
			Comment("对基础货币汇率（手动 + 可选定时拉取，M3 插件）"),
		field.Bool("enabled").Default(true),
		field.Int32("sort").Default(0),
	}
}

func (Currency) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("enabled"),
	}
}
