package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Shipment struct{ ent.Schema }

func (Shipment) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }
func (Shipment) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"), field.Uint64("order_id"),
		field.String("carrier").MaxLen(100), field.String("tracking_no").MaxLen(100),
		field.JSON("items", map[string]int32{}),
		field.JSON("address", map[string]string{}),
		field.String("status").Default("shipped"), field.Uint64("admin_id"),
		field.String("request_key").MaxLen(128).Unique(),
		field.Int64("received_at").Default(0),
	}
}
func (Shipment) Indexes() []ent.Index {
	return []ent.Index{index.Fields("order_id"), index.Fields("subsite_id", "status")}
}

type PhysicalStockMovement struct{ ent.Schema }

func (PhysicalStockMovement) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }
func (PhysicalStockMovement) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"), field.Uint64("product_id"), field.Uint64("sku_id").Default(0),
		field.Uint64("order_id").Default(0), field.Int64("delta"),
		field.String("reference").MaxLen(180).Unique(), field.String("reason").MaxLen(255),
	}
}
func (PhysicalStockMovement) Indexes() []ent.Index {
	return []ent.Index{index.Fields("product_id", "sku_id"), index.Fields("order_id")}
}
