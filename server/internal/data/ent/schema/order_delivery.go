package schema

// 所有权：mods/fulfillment（M1；表结构 M0 落地——2.0 关键改造：无明文快照）

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// OrderDelivery 交付记录（《数据库架构设计.md》§4.3）。
//
// 1.x 痛点反转：不再存明文卡密快照，改为「卡密引用 + 一次性取货令牌哈希」；
// 取货时现场解密返回、不落库明文（§5.20.2）；取货响应 Cache-Control: no-store。
type OrderDelivery struct {
	ent.Schema
}

func (OrderDelivery) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (OrderDelivery) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		// 硬外键 → orders
		field.Uint64("order_id"),
		field.Uint64("item_id").Comment("订单子项"),
		field.Uint64("card_id").Comment("卡密引用（现场解密，绝不存明文）"),
		field.String("delivery_token_hash").MaxLen(64).Comment("一次性取货令牌哈希"),
		field.Enum("delivered_mode").Values("status", "delete").Comment("标记/即删（即删 = 卡密物理删除）"),
		field.Uint64("delivered_by").Default(0).Comment("人工发货管理员（auto 为 0）"),
		field.JSON("logistics", map[string]any{}).Optional().Comment("结构化交付信息（物流单号/追踪链接）"),
		field.Int32("fetch_count").Default(0).Comment("已取货次数（默认 1 次后掩码）"),
		field.Time("delivered_at").SchemaType(mysqlTime).Optional(),
		field.String("fetched_ip").MaxLen(64).Optional().Comment("取货 IP（审计，不含明文）"),
	}
}

func (OrderDelivery) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("order_id"),
		index.Fields("card_id"),
	}
}

func (OrderDelivery) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("order", Order.Type).
			Ref("deliveries").
			Field("order_id").
			Required().
			Unique(),
	}
}
