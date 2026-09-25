package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ProductDeliverySource is a versioned shared receipt, independent of any buyer.
// Retiring current_key keeps existing order snapshots readable.
type ProductDeliverySource struct{ ent.Schema }

func (ProductDeliverySource) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }
func (ProductDeliverySource) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"), field.Uint64("product_id"), field.Uint64("sku_id").Default(0),
		field.String("purchase_key").MaxLen(64).Optional().Nillable().Unique(),
		field.String("current_key").MaxLen(100).Optional().Nillable(),
		field.String("status").MaxLen(20).Default("empty"),
		field.Bytes("content").Optional(),
		field.Uint64("connection_id").Default(0),
		field.String("upstream_product").MaxLen(128).Default(""),
		field.String("upstream_sku").MaxLen(128).Default(""),
		field.String("upstream_order_id").MaxLen(128).Default(""),
		field.String("connection_revision").MaxLen(64).Default(""),
		field.Uint64("origin_procurement_id").Default(0),
		field.Int64("expires_at").Default(0), field.Int64("delivered_count").Default(0),
		field.Int64("max_deliveries").Default(0),
		field.Int64("submitted_at").Default(0),
		field.Int64("next_check_at").Default(0), field.Float("exchange_rate").Default(1),
		field.Int64("cost_cents").Default(0),
		field.String("last_error").MaxLen(255).Default(""),
	}
}
func (ProductDeliverySource) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("current_key").Unique(), index.Fields("subsite_id", "product_id", "sku_id"),
		index.Fields("status", "next_check_at"),
	}
}
