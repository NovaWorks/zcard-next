package schema

// 所有权：platform/queue（无 Redis 降级的死信记录，规划 §4.8 降级矩阵）

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// FailedTask 无 Redis 降级模式的死信：SyncQueue 同步执行失败落库，
// 供手动重放（`zcard admin retry-task` M2 交付）与排障查询。
type FailedTask struct {
	ent.Schema
}

func (FailedTask) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("task_type").MaxLen(128).Comment("任务类型（event:order.paid / card:import...）"),
		field.Bytes("payload").Optional().Comment("任务载荷"),
		field.Text("error").Optional().Comment("失败原因"),
		field.Int32("retry_count").Default(0).Comment("已重试次数"),
		field.Enum("status").Values("pending", "done").Default("pending").Comment("pending=待重放；done=已重放"),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (FailedTask) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "created_at"),
		index.Fields("task_type"),
	}
}
