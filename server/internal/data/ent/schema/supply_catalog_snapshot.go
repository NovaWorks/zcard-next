package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SupplyCatalogSnapshot keeps catalog reads independent of an HTTP request.
// Ready payloads are immutable so another operator refreshing cannot change a selection.
type SupplyCatalogSnapshot struct{ ent.Schema }

func (SupplyCatalogSnapshot) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }
func (SupplyCatalogSnapshot) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"), field.String("token").MaxLen(36).Unique(),
		field.Uint64("connection_id"), field.String("identity").MaxLen(64),
		field.String("status").MaxLen(20).Default("pending"),
		field.String("lease_token").Default(""), field.Int64("lease_until").Default(0),
		field.Int64("expires_at"), field.Int("attempts").Default(0),
		field.Int("loaded_count").Default(0),
		field.String("message").MaxLen(500).Default(""),
		field.JSON("payload", json.RawMessage{}).Optional(),
	}
}
func (SupplyCatalogSnapshot) Indexes() []ent.Index {
	return []ent.Index{index.Fields("connection_id", "subsite_id", "created_at"), index.Fields("expires_at"), index.Fields("status", "lease_until")}
}
