package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// ProductContentBatch persists an immutable preview and its idempotent result.
type ProductContentBatch struct{ ent.Schema }

func (ProductContentBatch) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }
func (ProductContentBatch) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("token").MaxLen(64).Unique(),
		field.Uint64("actor_id"),
		field.JSON("payload", json.RawMessage{}),
		field.Time("expires_at").SchemaType(mysqlTime),
		field.Bool("completed").Default(false),
		field.Int32("matched").Default(0),
		field.Int32("changed").Default(0),
		field.Int32("skipped_locked").Default(0),
	}
}
