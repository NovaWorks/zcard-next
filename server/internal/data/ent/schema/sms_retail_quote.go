package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SMSRetailQuote binds a retail price to one supplier quote and one member.
type SMSRetailQuote struct{ ent.Schema }

func (SMSRetailQuote) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }
func (SMSRetailQuote) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").MaxLen(64), field.Uint64("user_id"), field.Uint64("product_id"),
		field.Uint64("connection_id"), field.String("connection_identity"), field.String("pricing_revision"), field.Int64("product_revision"),
		field.String("upstream_quote_id").MaxLen(64), field.String("upstream_product_id"),
		field.Int64("cost_cents"), field.Int64("amount_cents"), field.Int64("expires_at"),
		field.String("offer_name"), field.JSON("selection", map[string]string{}).Optional(), field.String("consumed_by").Default(""),
	}
}
func (SMSRetailQuote) Indexes() []ent.Index {
	return []ent.Index{index.Fields("subsite_id", "user_id", "product_id"), index.Fields("expires_at")}
}
