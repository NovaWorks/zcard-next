-- Modify "products" table
ALTER TABLE "products" ADD COLUMN "product_kind" character varying NOT NULL DEFAULT 'standard';
-- Create "sms_retail_quotes" table
CREATE TABLE "sms_retail_quotes" ("id" character varying NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "subsite_id" bigint NOT NULL DEFAULT 0, "user_id" bigint NOT NULL, "product_id" bigint NOT NULL, "connection_id" bigint NOT NULL, "connection_identity" character varying NOT NULL, "pricing_revision" character varying NOT NULL, "product_revision" bigint NOT NULL, "upstream_quote_id" character varying NOT NULL, "upstream_product_id" character varying NOT NULL, "cost_cents" bigint NOT NULL, "amount_cents" bigint NOT NULL, "expires_at" bigint NOT NULL, "offer_name" character varying NOT NULL, "selection" jsonb NULL, "consumed_by" character varying NOT NULL DEFAULT '', PRIMARY KEY ("id"));
-- Create index "smsretailquote_expires_at" to table: "sms_retail_quotes"
CREATE INDEX "smsretailquote_expires_at" ON "sms_retail_quotes" ("expires_at");
-- Create index "smsretailquote_subsite_id_user_id_product_id" to table: "sms_retail_quotes"
CREATE INDEX "smsretailquote_subsite_id_user_id_product_id" ON "sms_retail_quotes" ("subsite_id", "user_id", "product_id");
