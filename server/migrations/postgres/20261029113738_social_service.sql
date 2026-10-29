-- Modify "order_items" table
ALTER TABLE "order_items" ADD COLUMN "social_purchase_snapshot" text NOT NULL DEFAULT '', ADD COLUMN "social_schema" jsonb NULL;
-- Modify "products" table
ALTER TABLE "products" ADD COLUMN "social_schema" jsonb NULL;
-- Create "social_operations" table
CREATE TABLE "social_operations" ("id" character varying NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "subsite_id" bigint NOT NULL DEFAULT 0, "task_id" character varying NOT NULL, "operation_id" character varying NOT NULL, "action" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'pending', "upstream_operation_id" character varying NOT NULL DEFAULT '', "submit_started" boolean NOT NULL DEFAULT false, "amount_cents" bigint NOT NULL DEFAULT 0, "reference" character varying NOT NULL DEFAULT '', "operator_id" bigint NOT NULL DEFAULT 0, "reason" character varying NOT NULL DEFAULT '', "attempts" bigint NOT NULL DEFAULT 0, "error_code" character varying NOT NULL DEFAULT '', PRIMARY KEY ("id"));
-- Create index "socialoperation_task_id_operation_id" to table: "social_operations"
CREATE UNIQUE INDEX "socialoperation_task_id_operation_id" ON "social_operations" ("task_id", "operation_id");
-- Create index "socialoperation_task_id_status" to table: "social_operations"
CREATE INDEX "socialoperation_task_id_status" ON "social_operations" ("task_id", "status");
-- Create "social_quotes" table
CREATE TABLE "social_quotes" ("id" character varying NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "subsite_id" bigint NOT NULL DEFAULT 0, "role" character varying NOT NULL DEFAULT 'wholesale', "account_id" bigint NOT NULL DEFAULT 0, "user_id" bigint NOT NULL DEFAULT 0, "product_id" bigint NOT NULL, "connection_id" bigint NOT NULL, "connection_identity" character varying NOT NULL DEFAULT '', "connection_revision" character varying NOT NULL DEFAULT '', "product_revision" bigint NOT NULL DEFAULT 0, "schema_revision" character varying NOT NULL, "pricing_revision" character varying NOT NULL DEFAULT '', "upstream_product_id" character varying NOT NULL, "params_json" text NOT NULL DEFAULT '', "params_cipher" bytea NULL, "schema_json" text NOT NULL DEFAULT '', "supply_quote_id" character varying NOT NULL DEFAULT '', "currency" character varying NOT NULL DEFAULT 'CNY', "amount_cents" bigint NOT NULL, "cost_cents" bigint NOT NULL DEFAULT 0, "expires_at" bigint NOT NULL, "consumed_by" character varying NOT NULL DEFAULT '', PRIMARY KEY ("id"));
-- Create index "socialquote_expires_at" to table: "social_quotes"
CREATE INDEX "socialquote_expires_at" ON "social_quotes" ("expires_at");
-- Create index "socialquote_role_account_id_user_id_product_id" to table: "social_quotes"
CREATE INDEX "socialquote_role_account_id_user_id_product_id" ON "social_quotes" ("role", "account_id", "user_id", "product_id");
-- Create "social_tasks" table
CREATE TABLE "social_tasks" ("id" character varying NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "subsite_id" bigint NOT NULL DEFAULT 0, "role" character varying NOT NULL DEFAULT 'wholesale', "supply_order_id" bigint NOT NULL DEFAULT 0, "order_id" bigint NOT NULL DEFAULT 0, "order_item_id" bigint NOT NULL DEFAULT 0, "account_id" bigint NOT NULL DEFAULT 0, "user_id" bigint NOT NULL DEFAULT 0, "product_id" bigint NOT NULL DEFAULT 0, "connection_id" bigint NOT NULL, "connection_identity" character varying NOT NULL DEFAULT '', "connection_revision" character varying NOT NULL DEFAULT '', "quote_id" character varying NOT NULL, "schema_revision" character varying NOT NULL, "request_no" character varying NOT NULL, "request_hash" character varying NOT NULL, "request_json" text NOT NULL DEFAULT '', "request_cipher" bytea NULL, "upstream_product_id" character varying NOT NULL, "upstream_order_id" character varying NOT NULL DEFAULT '', "upstream_operation_id" character varying NOT NULL DEFAULT '', "phase" character varying NOT NULL DEFAULT 'purchase', "state" character varying NOT NULL DEFAULT 'pending', "submit_started" boolean NOT NULL DEFAULT false, "submit_started_at" bigint NOT NULL DEFAULT 0, "version" bigint NOT NULL DEFAULT 1, "upstream_version" bigint NOT NULL DEFAULT 0, "snapshot_cipher" bytea NULL, "currency" character varying NOT NULL DEFAULT 'CNY', "charged_amount" bigint NOT NULL DEFAULT 0, "cost_cents" bigint NOT NULL DEFAULT 0, "settlement_state" character varying NOT NULL DEFAULT 'paid', "refunded_amount" bigint NOT NULL DEFAULT 0, "refund_reference" character varying NOT NULL DEFAULT '', "retail_paid_amount" bigint NOT NULL DEFAULT 0, "retail_refunded_amount" bigint NOT NULL DEFAULT 0, "retail_refund_state" character varying NOT NULL DEFAULT 'none', "refund_id" bigint NOT NULL DEFAULT 0, "can_cancel" boolean NOT NULL DEFAULT false, "can_refill" boolean NOT NULL DEFAULT false, "next_run_at" bigint NOT NULL DEFAULT 0, "lease_until" bigint NOT NULL DEFAULT 0, "lease_token" character varying NOT NULL DEFAULT '', "attempts" bigint NOT NULL DEFAULT 0, "last_error" character varying NOT NULL DEFAULT '', PRIMARY KEY ("id"));
-- Create index "socialtask_account_id" to table: "social_tasks"
CREATE INDEX "socialtask_account_id" ON "social_tasks" ("account_id");
-- Create index "socialtask_connection_id_upstream_order_id" to table: "social_tasks"
CREATE INDEX "socialtask_connection_id_upstream_order_id" ON "social_tasks" ("connection_id", "upstream_order_id");
-- Create index "socialtask_role_account_id_request_no" to table: "social_tasks"
CREATE UNIQUE INDEX "socialtask_role_account_id_request_no" ON "social_tasks" ("role", "account_id", "request_no");
-- Create index "socialtask_role_order_item_id" to table: "social_tasks"
CREATE INDEX "socialtask_role_order_item_id" ON "social_tasks" ("role", "order_item_id");
-- Create index "socialtask_role_phase_next_run_at_lease_until" to table: "social_tasks"
CREATE INDEX "socialtask_role_phase_next_run_at_lease_until" ON "social_tasks" ("role", "phase", "next_run_at", "lease_until");
-- Create index "socialtask_role_supply_order_id" to table: "social_tasks"
CREATE INDEX "socialtask_role_supply_order_id" ON "social_tasks" ("role", "supply_order_id");
-- Create index "socialtask_subsite_id_user_id_order_id" to table: "social_tasks"
CREATE INDEX "socialtask_subsite_id_user_id_order_id" ON "social_tasks" ("subsite_id", "user_id", "order_id");
