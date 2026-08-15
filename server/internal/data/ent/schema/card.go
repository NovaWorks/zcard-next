package schema

// 所有权：mods/inventory（M1；表结构 M0 落地——铁律 11：卡密永不落明文）

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Card 卡密/链接/兑换码（最高频、最可能破千万的表，《数据库架构设计.md》§4.2）。
//
// 安全要点（铁律 11/§5.20.2）：
//   - content 强制 AES-256-GCM（随机 nonce + AAD 绑定 product_id/tenant），无关闭开关；
//   - content_hash = HMAC-SHA256(cardKey, plain) keyed hash，防低熵卡彩虹表反推；
//   - 售出后按 delivery_mode 走「标记/即删」，即删模式物理删除（不留密文残骸）；
//   - 乐观锁 version 与「FOR UPDATE 锁卡 + affected rows 校验」双保险（SQLite 走 BEGIN IMMEDIATE + CAS）。
type Card struct {
	ent.Schema
}

func (Card) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}, TenantMixin{}, VersionMixin{}}
}

func (Card) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("product_id").Comment("所属商品（硬外键 → products）"),
		field.Bytes("content").Comment("AES-256-GCM 密文（AAD 绑定 product_id/subsite_id）"),
		field.String("content_hash").MaxLen(64).Comment("keyed hash = HMAC-SHA256(cardKey, plain)，商品内去重"),
		field.String("number_hash").MaxLen(64).Optional().Comment("靓号/卡号归一化 hash（靓号自选检索）"),
		field.Enum("status").Values("available", "reserved", "used", "disabled").Default("available"),
		// 软外键：锁定/售出订单反查（有意不加 FK，防 cards↔orders 环）
		field.Uint64("order_id").Optional().Comment("锁定/售出订单（软外键，仅索引）"),
		field.Uint64("sku_id").Optional().Comment("所属 SKU（可空=商品级）"),
		field.Uint64("import_id").Optional().Comment("导入批次"),
		field.String("card_type").MaxLen(20).Optional().Comment("卡密类型（月卡/周卡，对应 SKU）"),
		field.String("note").MaxLen(255).Optional(),
		field.Uint64("owner_id").Default(0).Comment("所属会员（0=系统；会员预选/锁定靓号）"),
		field.Int64("draft_premium").Default(0).Comment("预选加价（分）"),
		field.Int64("draft_cost").Default(0).Comment("预选成本（分）"),
		field.Int64("price").Optional().Comment("靓号自选价（分，NULL=商品价）"),
		field.Time("locked_at").SchemaType(mysqlTime).Optional().Comment("锁定时间（TTL 释放）"),
		field.Time("used_at").SchemaType(mysqlTime).Optional().Comment("售出时间"),
	}
}

func (Card) Indexes() []ent.Index {
	return []ent.Index{
		// 唯一索引必须含 subsite_id（§4.11.5），避免分站间误判重复
		index.Fields("subsite_id", "product_id", "content_hash").Unique(),
		// 发货热路径：SELECT ... WHERE product_id=? AND status='available' FOR UPDATE
		index.Fields("product_id", "status"),
		index.Fields("subsite_id", "status"),
		index.Fields("order_id"),
		index.Fields("import_id"),
	}
}

func (Card) Edges() []ent.Edge {
	return []ent.Edge{
		// 硬外键 → products（核心聚合内，数据库架构 §6 关系规则 1）
		edge.From("product", Product.Type).
			Ref("cards").
			Field("product_id").
			Required().
			Unique(),
	}
}
