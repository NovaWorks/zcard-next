package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type PluginData struct{ ent.Schema }

func (PluginData) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (PluginData) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"), field.String("plugin_id").MaxLen(64),
		field.Uint64("subsite_id"), field.String("entity_type").MaxLen(24),
		field.Uint64("entity_id"), field.String("key").MaxLen(32),
		field.Bytes("payload"), field.Int("schema_version").Default(1),
		field.Int64("revision").Default(0).NonNegative(),
	}
}
func (PluginData) Indexes() []ent.Index {
	return []ent.Index{index.Fields("plugin_id", "subsite_id", "entity_type", "entity_id", "key").Unique()}
}
