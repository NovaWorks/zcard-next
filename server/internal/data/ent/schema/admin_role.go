package schema

// 所有权：mods/authz（M0：自建 RBAC，权限目录自动生成 + 内置角色种子）

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AdminRole 角色/权限组。权限目录由启动时从 Kratos 路由表自动生成（§5.14），
// 不建独立权限表；role_permissions 存权限点编码。
type AdminRole struct {
	ent.Schema
}

func (AdminRole) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (AdminRole) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("name").MaxLen(60).Unique(),
		field.String("code").MaxLen(60).Unique().Comment("super_admin/operator/support/..."),
		field.String("description").MaxLen(255).Optional(),
		// 内置角色种子由代码维护，后台不可删除
		field.Bool("is_builtin").Default(false),
	}
}

// RolePermission 角色-权限点（多对多）。权限点如 order:list / card:view_content / order:refund。
// 敏感权限点独立拆分（防内部偷卡 §5.20.4），rbac_coverage_test 保证每条管理路由被角色覆盖（§4.10-8）。
type RolePermission struct {
	ent.Schema
}

func (RolePermission) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (RolePermission) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		// 硬外键 → admin_roles（聚合内）
		field.Uint64("role_id"),
		field.String("permission_code").MaxLen(100),
	}
}

func (RolePermission) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("role_id", "permission_code").Unique(),
	}
}
