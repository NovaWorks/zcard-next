package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// InstalledPlugin is instance state, not a marketplace listing or license.
type InstalledPlugin struct{ ent.Schema }

func (InstalledPlugin) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (InstalledPlugin) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("plugin_id").MaxLen(64).Unique(),
		field.Bool("desired_enabled").Default(false),
		field.Int64("desired_generation").Default(0).NonNegative(),
		field.Int64("observed_generation").Default(0).NonNegative(),
		field.String("desired_digest").MaxLen(64).Default(""),
		field.String("observed_digest").MaxLen(64).Default(""),
		field.JSON("approved_scopes", []string{}).Optional(),
		field.JSON("block_reasons", []string{}).Optional(),
		field.String("current_operation_id").MaxLen(36).Default(""),
		field.Bool("uninstalled").Default(false),
	}
}
