package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SMSIntent owns one immutable purchase, its encrypted aggregate snapshot, and recovery lease.
// No cascading edges: catalog/user cleanup must never erase unsettled financial obligations.
type SMSIntent struct{ ent.Schema }

func (SMSIntent) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }
func (SMSIntent) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"), field.Uint64("order_id"), field.Uint64("order_item_id"), field.Uint64("user_id"),
		field.Uint64("connection_id"), field.String("connection_identity"),
		field.String("request_no").MaxLen(64), field.Text("request_json"), field.String("request_hash").MaxLen(64),
		field.String("upstream_order_id").Optional().Nillable(),
		field.String("phase").Default("purchase"), field.String("state").Default("allocating"),
		field.String("session_id").Default(""), field.Int64("version").Default(0), field.Int64("sms_revision").Default(0),
		field.Bytes("snapshot_cipher").Optional(), field.Bool("received").Default(false),
		field.Bool("can_cancel").Default(false), field.Bool("can_finish").Default(false),
		field.Int64("charged_amount").Default(0), field.String("settlement_state").Default(""),
		field.Int64("refunded_amount").Default(0), field.String("refund_reference").Default(""),
		field.Bool("rejected_receipt").Default(false),
		field.String("retail_refund_state").Default("none"), field.Uint64("refund_id").Default(0),
		field.Int64("next_run_at").Default(0), field.Int64("lease_until").Default(0), field.String("lease_token").Default(""),
		field.Int("attempts").Default(0), field.String("last_error").Default(""),
	}
}
func (SMSIntent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("order_item_id").Unique(), index.Fields("request_no").Unique(),
		index.Fields("connection_id", "upstream_order_id").Unique(), index.Fields("phase", "next_run_at", "lease_until"),
		index.Fields("subsite_id", "user_id", "order_id"),
	}
}

type SMSOperation struct{ ent.Schema }

func (SMSOperation) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }
func (SMSOperation) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"), field.Uint64("intent_id"), field.String("operation_id").MaxLen(64),
		field.String("action"), field.String("status").Default("pending"), field.String("error_code").Default(""),
		field.Int("attempts").Default(0),
	}
}
func (SMSOperation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("operation_id").Unique(), index.Fields("intent_id", "status")}
}
