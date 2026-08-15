package schema

// 所有权：mods/payment（M1；表结构 M0 落地）

import (
	"encoding/json"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// PaymentChannel 渠道配置（《数据库架构设计.md》§4.4）。
// 凭据 AES-GCM 加密存储；解密失败必须降级为空并提示重配，列表接口绝不 500（铁律 5）。
type PaymentChannel struct {
	ent.Schema
}

func (PaymentChannel) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }

func (PaymentChannel) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("name").MaxLen(60),
		field.String("code").MaxLen(30).Comment("渠道码（alipay/wechat/epay/wallet...）"),
		field.String("driver").MaxLen(100).Comment("驱动类型（适配器按 (provider, channel) 注册表路由）"),
		field.Bytes("config").Comment("AES-256-GCM 加密凭据（结构随 driver）"),
		field.Int64("fee").Default(0).Comment("手续费（分 或 万分比，按 fee_type）"),
		field.Enum("fee_type").Values("percent", "fixed").Default("fixed"),
		field.Enum("fee_bearer").Values("merchant", "user").Default("merchant").Comment("手续费承担方"),
		field.Int32("sort").Default(0),
		field.Bool("enabled").Default(true),
	}
}

func (PaymentChannel) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("subsite_id", "code").Unique(),
	}
}

// Payment 支付单（§4.4）。channel_order_no 隔离下游回传格式；
// UNIQUE(channel, channel_order_no) 为回调幂等核对锚点之一（回调幂等三层：状态机/行锁二次校验/业务幂等键）。
type Payment struct {
	ent.Schema
}

func (Payment) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }

func (Payment) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("order_id").Comment("关联订单（硬外键 → orders）"),
		field.String("channel").MaxLen(50).Comment("渠道码"),
		field.String("channel_order_no").MaxLen(80).Optional().Comment("网关单号（回调时回填）"),
		field.Int64("amount").Comment("应收（分，基础货币）"),
		field.Int64("charged_amount").Default(0).Comment("实收（分，回调核对；金额核对永远对基础货币）"),
		field.Int64("fee").Default(0).Comment("手续费（分）"),
		field.Enum("status").Values("pending", "success", "failed").Default("pending"),
		field.Time("paid_at").SchemaType(mysqlTime).Optional(),
		field.JSON("raw", json.RawMessage{}).Optional().Comment("回调原文（审计；不含敏感凭据）"),
		field.String("idempotency_key").MaxLen(64).Optional().Comment("业务幂等键（写接口 Idempotency-Key 头）"),
	}
}

func (Payment) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("order_id"),
		index.Fields("channel", "channel_order_no").Unique(),
		index.Fields("status", "created_at"),
	}
}

func (Payment) Edges() []ent.Edge {
	return []ent.Edge{
		// 硬外键 → orders（核心聚合内）
		edge.From("order", Order.Type).
			Ref("payments").
			Field("order_id").
			Required().
			Unique(),
	}
}

// RefundOrder 退款单——2.0 新增退款编排（§5.5.4）：渠道原路 / 钱包 / 上游传导三通道。
type RefundOrder struct {
	ent.Schema
}

func (RefundOrder) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (RefundOrder) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("order_id"),
		field.Int64("amount").Comment("退款金额（分，可部分退款）"),
		field.Enum("channel").Values("gateway", "wallet", "upstream").Comment("退款通道"),
		field.Enum("status").Values("created", "processing", "succeeded", "failed").Default("created"),
		field.Text("reason").Optional(),
		field.Uint64("operator_id").Optional().Comment("操作者（需 order:refund 权限 + 二次确认 + 审计）"),
		field.String("upstream_refund_id").MaxLen(64).Optional().Comment("上游退款单号（传导，M2）"),
	}
}

func (RefundOrder) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("order_id"),
		index.Fields("status"),
	}
}

func (RefundOrder) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("order", Order.Type).
			Ref("refunds").
			Field("order_id").
			Required().
			Unique(),
	}
}
