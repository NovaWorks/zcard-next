package schema

// 所有权：mods/identity（P0-02）/ mods/audit（P2-06）
// 字段对齐《数据库架构设计.md》§4/§5；业务功能按各任务书里程碑开发。

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Session 会话（access/refresh 双令牌中的 refresh 载体：14d 轮换、一次性、可吊销）。
type Session struct {
	ent.Schema
}

func (Session) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Enum("realm").Values("admin", "user").Comment("双 realm：admin_users.id 或 users.id"),
		field.Uint64("user_id").Comment("按 realm 指向 admin_users/users"),
		field.String("refresh_token_hash").MaxLen(128).Comment("refresh 令牌哈希（SHA-256，明文绝不落库）"),
		field.String("device").MaxLen(120).Optional(),
		field.String("ip").MaxLen(64).Optional(),
		field.String("user_agent").MaxLen(255).Optional(),
		field.Time("expires_at").SchemaType(mysqlTime),
		field.Time("revoked_at").SchemaType(mysqlTime).Optional(),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
		field.Time("updated_at").SchemaType(mysqlTime).Default(nowUTC).UpdateDefault(nowUTC),
	}
}

func (Session) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("refresh_token_hash").Unique(),
		index.Fields("realm", "user_id"),
		index.Fields("expires_at"),
	}
}

// UserGroup 1.x 用户组兼容映射表：仅 migrate-from-v1 写入、memberlevel 读取，业务不写。
type UserGroup struct {
	ent.Schema
}

func (UserGroup) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (UserGroup) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("code").MaxLen(60).Unique().Comment("1.x 组标识"),
		field.String("name").MaxLen(60),
		field.Uint64("level_id").Optional().Comment("映射 member_levels.id"),
	}
}

// ExternalIdentity 第三方登录绑定（UNIQUE(provider, provider_user_id)；M3 接 OAuth）。
type ExternalIdentity struct {
	ent.Schema
}

func (ExternalIdentity) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ExternalIdentity) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("user_id"),
		field.String("provider").MaxLen(32).Comment("telegram/google/..."),
		field.String("provider_user_id").MaxLen(128),
		field.String("username").MaxLen(120).Optional(),
		field.String("avatar_url").MaxLen(255).Optional(),
		field.Time("auth_at").SchemaType(mysqlTime).Optional(),
	}
}

func (ExternalIdentity) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("provider", "provider_user_id").Unique(),
		index.Fields("user_id"),
	}
}

// EmailVerification 邮箱验证码（purpose 区分注册/找回；attempt 上限防爆破）。
type EmailVerification struct {
	ent.Schema
}

func (EmailVerification) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("email").MaxLen(255),
		field.Uint64("user_id").Optional(),
		field.Enum("purpose").Values("register", "reset"),
		field.String("code_hash").MaxLen(128).Comment("验证码哈希"),
		field.Time("expires_at").SchemaType(mysqlTime),
		field.Time("verified_at").SchemaType(mysqlTime).Optional(),
		field.Int32("attempt_count").Default(0),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (EmailVerification) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("email", "purpose", "created_at"),
	}
}

// AuditLog 管理员操作审计（变更类 admin 操作：操作者/权限点/前后快照/IP）。
type AuditLog struct {
	ent.Schema
}

func (AuditLog) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Enum("operator_type").Values("admin", "user", "system"),
		field.Uint64("operator_id").Optional(),
		field.String("permission_point").MaxLen(100).Optional(),
		field.String("action").MaxLen(10).Comment("POST/PUT/DELETE"),
		field.String("route").MaxLen(255),
		field.JSON("before", map[string]any{}).Optional().Comment("变更前快照"),
		field.JSON("after", map[string]any{}).Optional().Comment("变更后快照"),
		field.String("ip").MaxLen(64).Optional(),
		field.String("user_agent").MaxLen(255).Optional(),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (AuditLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("operator_type", "operator_id", "created_at"),
		index.Fields("created_at"),
	}
}

// SecurityAuditLog 安全审计（登录失败/异地/敏感操作——解密/取货/导出/权限变更/密钥轮换）。
type SecurityAuditLog struct {
	ent.Schema
}

func (SecurityAuditLog) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Enum("actor_type").Values("admin", "user", "guest", "system"),
		field.Uint64("actor_id").Optional(),
		field.String("action").MaxLen(255).Comment("事件名（identity.login_failed / card.decrypt ...）"),
		field.String("ip").MaxLen(64).Optional(),
		field.JSON("metadata", map[string]any{}).Optional().Comment("关键 ID 等（明文卡密/凭据绝不入内）"),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (SecurityAuditLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("actor_type", "actor_id", "created_at"),
		index.Fields("action", "created_at"),
	}
}

// VisitLog 访问统计（轻量聚合：按小时×路径聚合行，不记明细）。
type VisitLog struct {
	ent.Schema
}

func (VisitLog) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (VisitLog) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("subsite_id").Default(0),
		field.String("stat_date").MaxLen(8).Comment("yyyymmdd"),
		field.Int8("stat_hour").Comment("0-23"),
		field.String("path").MaxLen(255).Comment("路径或来源标识"),
		field.Int64("pv").Default(0),
		field.Int64("uv").Default(0),
	}
}

func (VisitLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("subsite_id", "stat_date", "stat_hour", "path").Unique(),
	}
}

// RiskLockKey 风控锁定（IP 维度哈希键 + TTL；下单 pending 闸门/取货失败锁定）。
// 注：库文档写 key_hash 为主键，实现取 uint64 自增主键 + key_hash 唯一索引
// （与全库主键策略一致，唯一性语义等价）。
type RiskLockKey struct {
	ent.Schema
}

func (RiskLockKey) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("key_hash").MaxLen(128).Comment("锁定键哈希（IP/订单维度，命名空间前缀）"),
		field.Time("expires_at").SchemaType(mysqlTime).Comment("TTL 过期自动失效"),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (RiskLockKey) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("key_hash").Unique(),
		index.Fields("expires_at"),
	}
}
