package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SocialQuote binds an immutable service selection and price to its purchaser.
// Parameter and request ciphers use the quote/task ID as authenticated context.
type SocialQuote struct{ ent.Schema }

func (SocialQuote) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }
func (SocialQuote) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").MaxLen(64), field.String("role").Default("wholesale"),
		field.Uint64("account_id").Default(0), field.Uint64("user_id").Default(0), field.Uint64("product_id"),
		field.Uint64("connection_id"), field.String("connection_identity").Default(""), field.String("connection_revision").Default(""),
		field.Int64("product_revision").Default(0), field.String("schema_revision"), field.String("pricing_revision").Default(""),
		field.String("upstream_product_id"), field.Text("params_json").Default(""), field.Bytes("params_cipher").Optional(),
		field.Text("schema_json").Default(""), field.String("supply_quote_id").Default(""),
		field.String("currency").Default("CNY"), field.Int64("amount_cents"), field.Int64("cost_cents").Default(0),
		field.Int64("expires_at"), field.String("consumed_by").Default(""),
	}
}
func (SocialQuote) Indexes() []ent.Index {
	return []ent.Index{index.Fields("role", "account_id", "user_id", "product_id"), index.Fields("expires_at")}
}

// SocialTask owns one purchase obligation. Catalog cleanup must not cascade to it.
type SocialTask struct{ ent.Schema }

func (SocialTask) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }
func (SocialTask) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").MaxLen(64), field.String("role").Default("wholesale"),
		field.Uint64("supply_order_id").Default(0), field.Uint64("order_id").Default(0), field.Uint64("order_item_id").Default(0),
		field.Uint64("account_id").Default(0), field.Uint64("user_id").Default(0), field.Uint64("product_id").Default(0),
		field.Uint64("connection_id"), field.String("connection_identity").Default(""), field.String("connection_revision").Default(""),
		field.String("quote_id"), field.String("schema_revision"), field.String("request_no").MaxLen(64), field.String("request_hash").MaxLen(64),
		field.Text("request_json").Default(""), field.Bytes("request_cipher").Optional(), field.String("upstream_product_id"),
		field.String("upstream_order_id").Default(""), field.String("upstream_operation_id").Default(""),
		field.String("phase").Default("purchase"), field.String("state").Default("pending"),
		field.Bool("submit_started").Default(false), field.Int64("submit_started_at").Default(0),
		field.Int64("version").Default(1), field.Int64("upstream_version").Default(0), field.Bytes("snapshot_cipher").Optional(),
		field.String("currency").Default("CNY"), field.Int64("charged_amount").Default(0), field.Int64("cost_cents").Default(0),
		field.String("settlement_state").Default("paid"), field.Int64("refunded_amount").Default(0), field.String("refund_reference").Default(""),
		field.Int64("retail_paid_amount").Default(0), field.Int64("retail_refunded_amount").Default(0),
		field.String("retail_refund_state").Default("none"), field.Uint64("refund_id").Default(0),
		field.Bool("can_cancel").Default(false), field.Bool("can_refill").Default(false),
		field.Int64("next_run_at").Default(0), field.Int64("lease_until").Default(0), field.String("lease_token").Default(""),
		field.Int("attempts").Default(0), field.String("last_error").Default(""),
	}
}
func (SocialTask) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("role", "account_id", "request_no").Unique(), index.Fields("role", "supply_order_id"), index.Fields("role", "order_item_id"),
		index.Fields("connection_id", "upstream_order_id"), index.Fields("role", "phase", "next_run_at", "lease_until"),
		index.Fields("subsite_id", "user_id", "order_id"), index.Fields("account_id"),
	}
}

type SocialOperation struct{ ent.Schema }

func (SocialOperation) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }
func (SocialOperation) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").MaxLen(64), field.String("task_id").MaxLen(64), field.String("operation_id").MaxLen(64),
		field.String("action"), field.String("status").Default("pending"), field.String("upstream_operation_id").Default(""),
		field.Bool("submit_started").Default(false), field.Int64("amount_cents").Default(0), field.String("reference").Default(""),
		field.Uint64("operator_id").Default(0), field.String("reason").Default(""),
		field.Int("attempts").Default(0), field.String("error_code").Default(""),
	}
}
func (SocialOperation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("task_id", "operation_id").Unique(), index.Fields("task_id", "status")}
}
