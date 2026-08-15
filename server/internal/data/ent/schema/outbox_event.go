package schema

// 所有权：platform/events（事务性 Outbox，规划 §4.8；M1 交付 relay）

import (
	"encoding/json"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// OutboxEvent 事务性 Outbox 事件：业务事务内写入，relay（500ms 扫描）投递 asynq 或进程内分发。
// dedupe_key 唯一索引天然防重复发布（如订单重复回调只发一次 order.paid）。
type OutboxEvent struct {
	ent.Schema
}

func (OutboxEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("module").MaxLen(32).Comment("发布模块（order/payment/...）"),
		field.String("type").MaxLen(64).Comment("事件类型（order.paid 等，目录见附录 C）"),
		field.String("aggregate_id").MaxLen(64).Comment("聚合根 ID（订单号/支付单号）"),
		field.JSON("payload", json.RawMessage{}).Optional().Comment("事件载荷（proto schema，只加字段不改语义）"),
		field.String("dedupe_key").MaxLen(120).Unique().Comment("防重复发布幂等键（order:123:paid）"),
		field.Enum("status").Values("publishing", "published", "failed").Default("publishing"),
		field.Time("published_at").SchemaType(mysqlTime).Optional(),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (OutboxEvent) Indexes() []ent.Index {
	return []ent.Index{
		// relay 扫描热路径
		index.Fields("status", "created_at"),
	}
}

// ProcessedEvent 消费幂等：消费成功即记录，重复投递直接 ACK（UNIQUE(event_id, consumer)）。
type ProcessedEvent struct {
	ent.Schema
}

func (ProcessedEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("event_id").Comment("outbox_events.id"),
		field.String("consumer").MaxLen(64).Comment("消费者标识（模块.处理器名）"),
		field.Time("processed_at").SchemaType(mysqlTime).Default(nowUTC),
	}
}

func (ProcessedEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("event_id", "consumer").Unique(),
	}
}
