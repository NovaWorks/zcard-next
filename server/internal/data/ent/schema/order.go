package schema

// 所有权：mods/order（M1；表结构 M0 落地）

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Order 父订单（一个交易单元：金额/支付/联系方式/查询密码，《数据库架构设计.md》§4.3）。
// 父子单模型：父单承载交易，子项（order_items）每商品一行；父状态由子状态聚合（CalcParentStatus）。
type Order struct {
	ent.Schema
}

func (Order) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}, TenantMixin{}, VersionMixin{}}
}

func (Order) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		// 对外单号 = 雪花 ID + 前缀，不可枚举（取货三重门之一，铁律 12）；与内部 id 严格分离
		field.String("order_no").MaxLen(40).Unique().Comment("对外单号（雪花 ID，platform/id 生成）"),
		field.String("subsite_domain").MaxLen(255).Optional().Comment("分站域名快照"),
		field.Int64("subsite_profit").Default(0).Comment("分站利润快照（分）"),
		field.Bool("profit_eligible").Default(true).Comment("是否产生分站利润（自购/上级链命中则 false）"),
		field.Uint64("user_id").Optional().Comment("下单用户（NULL=游客）"),
		field.String("guest_contact").MaxLen(150).Optional().Comment("游客联系方式"),
		// 查询密码（argon2 哈希；默认强制开启，constant-time 比对，铁律 12）
		field.String("query_password_hash").MaxLen(128).Optional(),
		field.Enum("status").
			Values(
				"pending_payment", "paid", "fulfilling", "partially_delivered",
				"delivered", "completed", "canceled", "expired",
				"refund_pending", "refunded",
			).
			Default("pending_payment").
			Comment("状态机见规划 §5.3；每次迁移落 order_status_events"),
		// total_amount = SUM(order_amount_lines.amount) 冗余快照（对账约束）
		field.Int64("total_amount").Default(0).Comment("应付总额（分）"),
		field.Int64("cost").Default(0).Comment("成本快照（分）"),
		field.String("base_currency").MaxLen(3).Optional().Comment("基础货币（默认 CNY）"),
		field.String("display_currency").MaxLen(3).Optional().Comment("展示货币（下单快照）"),
		field.Float("exchange_rate").
			Optional().
			SchemaType(map[string]string{
				dialect.MySQL:    "decimal(20,8)",
				dialect.Postgres: "numeric(20,8)",
				dialect.SQLite:   "real",
			}).
			Comment("汇率快照（仅快照，换算中间过程用 decimal 库）"),
		field.Int64("amount_display").Optional().Comment("展示货币金额（最小单位）"),
		field.String("payment_channel").MaxLen(30).Optional().Comment("支付渠道码快照"),
		field.String("contact").MaxLen(150).Optional().Comment("联系方式"),
		field.String("client_ip").MaxLen(64).Optional().Comment("下单 IP"),
		field.String("risk_ip").MaxLen(80).Optional().Comment("规范化风控 IP（IPv6 按 /64 聚合）"),
		field.JSON("risk_flags", map[string]any{}).Optional().Comment("风控标记（M3 填充）"),
		field.Uint64("parent_id").Optional().Comment("子单归属父单（父子单模型）"),
		// 商业版挂载点（M4 担保交易）：开源版恒 NULL，仅预留列
		field.Uint64("escrow_id").Optional().Comment("担保交易关联（商业版，M4）"),
		field.Uint64("invite_l1").Optional().Comment("三级分销归因链快照（下单瞬间锁定）"),
		field.Uint64("invite_l2").Optional(),
		field.Uint64("invite_l3").Optional(),
		field.JSON("extra", map[string]any{}).Optional().Comment("扩展预留（控件答案等，加字段先进 extra）"),
		field.Time("paid_at").Optional(),
		field.Time("closed_at").Optional(),
		field.Time("expired_at").Optional().Comment("超时取消扫描（INDEX(status, expired_at)）"),
	}
}

func (Order) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("subsite_id", "created_at"),
		index.Fields("user_id", "status"),
		index.Fields("status", "expired_at"),
		index.Fields("parent_id"),
		index.Fields("escrow_id"),
		index.Fields("invite_l1"),
		// 风控复合索引（友商 idx_orders_risk_pending 思路）
		index.Fields("risk_ip", "user_id", "status"),
	}
}

func (Order) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("items", OrderItem.Type).Comment("订单子项（每商品一行）"),
		edge.To("amount_lines", OrderAmountLine.Type).Comment("行式金额明细"),
		edge.To("status_events", OrderStatusEvent.Type).Comment("状态事件溯源"),
		edge.To("payments", Payment.Type).Comment("支付单"),
		edge.To("deliveries", OrderDelivery.Type).Comment("交付记录"),
		edge.To("refunds", RefundOrder.Type).Comment("退款单"),
	}
}

// OrderItem 订单子项（每商品一行，快照价 + 履约状态）。
type OrderItem struct {
	ent.Schema
}

func (OrderItem) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }

func (OrderItem) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("order_id").Comment("父订单（硬外键 → orders）"),
		field.Uint64("product_id"),
		field.Uint64("sku_id").Optional(),
		field.String("sku_name").MaxLen(100).Optional().Comment("SKU 名称快照"),
		field.Int64("unit_price").Comment("单价快照（分）"),
		field.Int32("quantity"),
		field.Int64("amount").Comment("小计（分）"),
		field.Int64("cost").Default(0).Comment("成本快照（分）"),
		field.Enum("fulfillment_type").Values("auto", "manual", "upstream").Comment("履约类型"),
		field.String("fulfillment_status").MaxLen(20).Default("pending").Comment("履约状态"),
		field.JSON("commission_snapshot", map[string]any{}).Optional().Comment("佣金快照（三级 + 费率）"),
		field.JSON("profit_snapshot", map[string]any{}).Optional().Comment("分站利润快照"),
	}
}

func (OrderItem) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("order_id"),
		index.Fields("product_id"),
	}
}

func (OrderItem) Edges() []ent.Edge {
	return []ent.Edge{
		// 硬外键 → orders（核心聚合内）
		edge.From("order", Order.Type).
			Ref("items").
			Field("order_id").
			Required().
			Unique(),
	}
}

// OrderAmountLine 订单金额明细行——行式金额模型，超越友商 20 个平铺 Amount 字段
// （数据库架构 §4.3）：价格计算管线每一步落一行，total_amount = SUM(amount)，
// 加折扣类型只加 type 枚举值、不加列；seq 对应管线顺序可重放。
type OrderAmountLine struct {
	ent.Schema
}

func (OrderAmountLine) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		// 硬外键 → orders
		field.Uint64("order_id"),
		field.Uint64("item_id").Optional().Comment("订单子项（NULL=父单级，如整单优惠券）"),
		field.Enum("type").
			Values(
				"base_price", "sku_adjust", "member_discount", "group_discount",
				"promo_discount", "coupon_discount", "points_discount",
				"subsite_markup", "fee", "tax", "rounding_adjust",
			).
			Comment("金额行类型（新增折扣类型只加枚举值）"),
		field.Int64("amount").Comment("有符号分（折扣为负、加价为正）"),
		field.String("source_type").MaxLen(32).Optional().Comment("来源类型 member_level/coupon/flash_sale/points/subsite/manual"),
		field.Uint64("source_id").Optional().Comment("来源 ID（优惠券/会员等级/秒杀）"),
		field.Int32("seq").Default(0).Comment("管线顺序（PriceCalculator 步骤序，可重放）"),
		field.JSON("meta", map[string]any{}).Optional().Comment("快照（费率/汇率/取整规则）"),
		field.Time("created_at").Immutable().Default(nowUTC),
	}
}

func (OrderAmountLine) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("order_id"),
		index.Fields("item_id"),
	}
}

func (OrderAmountLine) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("order", Order.Type).
			Ref("amount_lines").
			Field("order_id").
			Required().
			Unique(),
	}
}

// OrderStatusEvent 订单状态事件溯源——完整审计链「谁在何时把订单从 X 改成 Y、为什么」，
// 超越友商单一 status 字段（数据库架构 §4.3）；支撑对账/客服排查/风控回放。
type OrderStatusEvent struct {
	ent.Schema
}

func (OrderStatusEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("order_id"),
		field.String("from_status").MaxLen(20),
		field.String("to_status").MaxLen(20),
		field.String("event").MaxLen(32).Comment("created/paid/fulfilled/delivered/completed/canceled/expired/refund_requested/refunded"),
		field.Enum("operator").Values("system", "user", "admin", "worker"),
		field.Uint64("operator_id").Optional(),
		field.String("reason").MaxLen(255).Optional(),
		field.String("client_ip").MaxLen(64).Optional(),
		field.Time("created_at").Immutable().Default(nowUTC),
	}
}

func (OrderStatusEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("order_id", "created_at"),
		index.Fields("event"),
	}
}

func (OrderStatusEvent) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("order", Order.Type).
			Ref("status_events").
			Field("order_id").
			Required().
			Unique(),
	}
}
