package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type PluginOperation struct{ ent.Schema }

func (PluginOperation) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (PluginOperation) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("operation_id").MaxLen(36).Unique(),
		field.String("plugin_id").MaxLen(64),
		field.String("action").MaxLen(16),
		field.String("request_sha256").MaxLen(64),
		field.Uint64("actor_id"),
		field.Uint64("subsite_id"),
		field.Int64("expected_generation").NonNegative(),
		field.Int64("target_generation").Default(0).NonNegative(),
		field.String("target_digest").MaxLen(64).Default(""),
		field.JSON("approved_scopes", []string{}).Optional(),
		field.String("phase").MaxLen(24),
		field.String("failure_code").MaxLen(64).Default(""),
	}
}
func (PluginOperation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("plugin_id", "created_at")}
}
