package schema

// 所有权：mods/audit（T5 访问统计——PV/UV 明细 + 在线会话心跳；
// visit_logs 为按小时聚合表，page_views 为逐请求明细，二者并存互补）

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// PageView 访问明细（每请求一行：PV 计数 + UV 按 ip 去重）。
// 保留 90 天（cron audit.visit_cleanup 清理）；埋点失败不阻断业务请求。
type PageView struct {
	ent.Schema
}

func (PageView) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}, TenantMixin{}}
}

func (PageView) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("day").MaxLen(8).Comment("统计日 yyyymmdd（UTC）"),
		field.String("path").MaxLen(255).Comment("请求路径"),
		field.Uint64("user_id").Optional().Comment("登录用户（游客为 0）"),
		field.String("ip").MaxLen(64).Comment("客户端 IP"),
	}
}

func (PageView) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("day", "ip"),
		index.Fields("day", "subsite_id"),
	}
}

// UserSession 在线会话心跳（每登录用户一行，活跃即 upsert last_active_at）。
// 在线口径 = last_active_at 距今 ≤ 5 分钟；清理阈值 24 小时（cron）。
type UserSession struct {
	ent.Schema
}

func (UserSession) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}, TenantMixin{}}
}

func (UserSession) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("user_id"),
		field.String("ip").MaxLen(64).Comment("最近活跃 IP"),
		field.Time("last_active_at").SchemaType(mysqlTime).Comment("最近活跃时间（在线判定基准）"),
	}
}

func (UserSession) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("subsite_id", "user_id").Unique(),
		index.Fields("last_active_at"),
	}
}
