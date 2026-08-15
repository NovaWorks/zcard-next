// Package schema 为 Ent schema 唯一真理源（《数据库架构设计.md》§0）。
// 每个文件头部注释标注模块所有权（架构测试 §4.10-7 依据）。
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

// TimeMixin 通用时间字段（UTC 存储，展示时区由 settings 决定）。
type TimeMixin struct {
	mixin.Schema
}

func (TimeMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Time("created_at").
			Immutable().
			Default(nowUTC),
		field.Time("updated_at").
			Default(nowUTC).
			UpdateDefault(nowUTC),
	}
}

// TenantMixin 租户列（Row 行级隔离，开源版唯一启用的隔离模式，ADR-D15）。
//
// 三种隔离模式下统一保留该列（Schema/Database 模式恒为 0），Ent schema 定义不因模式而变。
// M1 交付 Ent interceptor 读写自动注入（读拦截器追加 subsite_id IN (ctx)、写拦截器从父实体
// 继承填充），业务代码不手写租户条件（铁律 14）。唯一索引必须含 subsite_id（§4.11.5）。
type TenantMixin struct {
	mixin.Schema
}

func (TenantMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("subsite_id").
			Default(0).
			Comment("租户（0=主站；由 interceptor 自动注入，业务不手写）"),
	}
}

// VersionMixin 乐观锁（并发行与行锁双保险，数据库架构 §3 通用字段约定）。
type VersionMixin struct {
	mixin.Schema
}

func (VersionMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Int32("version").Default(0).Comment("乐观锁版本号"),
	}
}
