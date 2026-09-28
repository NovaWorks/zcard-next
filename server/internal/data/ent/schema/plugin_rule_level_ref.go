package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type PluginRuleLevelRef struct{ ent.Schema }

func (PluginRuleLevelRef) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"), field.String("plugin_id").MaxLen(64),
		field.Uint64("subsite_id"), field.Uint64("product_id"), field.Uint64("level_id"),
	}
}
func (PluginRuleLevelRef) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("plugin_id", "subsite_id", "product_id", "level_id").Unique(), index.Fields("level_id"),
	}
}
