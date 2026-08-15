package schema

// 所有权：mods/identity + mods/authz（M0）

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AdminUser 员工账户（§5.14：头像/昵称/账号/密码/权限组/备注/启用/TOTP/最近登录）。
// 分站主登录分站后台 = AdminUser + subsite 隔离（M3），与顾客 User 严格分离。
type AdminUser struct {
	ent.Schema
}

func (AdminUser) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (AdminUser) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("username").MaxLen(60).Unique(),
		field.String("password_hash").MaxLen(255).Comment("bcrypt"),
		field.String("nickname").MaxLen(60).Optional(),
		field.String("avatar").MaxLen(255).Optional(),
		// 软外键 → admin_roles（仅索引；员工单角色，§5.14）
		field.Uint64("role_id").Comment("所属角色"),
		// TOTP 密钥（AES-GCM 加密存储，铁律：凭据加密；明文永不落库）
		field.Bytes("totp_secret").Optional().Comment("AES-256-GCM 加密的 TOTP 密钥"),
		field.Bool("enabled").Default(true),
		field.String("remark").MaxLen(255).Optional(),
		field.String("last_login_ip").MaxLen(64).Optional(),
		field.Time("last_login_at").SchemaType(mysqlTime).Optional(),
	}
}

func (AdminUser) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("role_id"),
	}
}
