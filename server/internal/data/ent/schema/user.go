package schema

// 所有权：mods/identity（M0）

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// User 顾客账户（全局表，无 subsite_id：分站共享用户体系，规划 §4.9）。
type User struct {
	ent.Schema
}

func (User) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("username").MaxLen(60).Unique(),
		field.String("email").MaxLen(255).Unique().Optional().Comment("第三方登录用户可无邮箱"),
		// bcrypt；与查询密码（orders.query_password_hash）相互独立
		field.String("password_hash").MaxLen(255).Optional().Comment("第三方登录用户可无密码"),
		field.Enum("status").Values("active", "banned", "deleted").Default("active"),
		field.Time("last_login_at").Optional(),
		// 三级分销归因链快照（注册/下单时绑定上级，供 affiliate 结算）
		field.Uint64("invite_l1").Optional(),
		field.Uint64("invite_l2").Optional(),
		field.Uint64("invite_l3").Optional(),
	}
}

func (User) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("invite_l1"),
	}
}
