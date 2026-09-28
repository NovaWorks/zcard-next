package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type PluginRequirement struct{ ent.Schema }

func (PluginRequirement) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (PluginRequirement) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"), field.String("plugin_id").MaxLen(64),
		field.Uint64("subsite_id"), field.Uint64("product_id"),
		field.Bool("required").Default(false), field.Int64("revision").Default(0).NonNegative(),
	}
}
func (PluginRequirement) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("plugin_id", "subsite_id", "product_id").Unique(),
		index.Fields("subsite_id", "product_id"),
	}
}
