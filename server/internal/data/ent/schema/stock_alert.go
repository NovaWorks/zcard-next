package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// StockAlert stores the notification episode for a locally sold stock slot.
// Notification delivery and this state commit together; no credentials are stored.
type StockAlert struct{ ent.Schema }

func (StockAlert) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (StockAlert) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"), field.Uint64("product_id"), field.Uint64("sku_id").Default(0),
		field.String("source_key").MaxLen(255), field.Int("threshold").Default(0),
		field.Int8("state").Default(0), field.Int8("notified_state").Default(0),
		field.Int64("last_notified_at").Default(0),
	}
}
func (StockAlert) Indexes() []ent.Index {
	return []ent.Index{index.Fields("product_id", "sku_id").Unique()}
}
