-- Modify "orders" table
ALTER TABLE "orders" ADD COLUMN "expiry_retry_at" timestamptz NULL, ADD COLUMN "expiry_attempts" integer NOT NULL DEFAULT 0, ADD COLUMN "expiry_review" boolean NOT NULL DEFAULT false, ADD COLUMN "expiry_reason" character varying NOT NULL DEFAULT '';
-- Modify "payments" table
ALTER TABLE "payments" ADD COLUMN "channel_id" bigint NOT NULL DEFAULT 0, ADD COLUMN "driver_snapshot" character varying NOT NULL DEFAULT '', ADD COLUMN "expires_at" timestamptz NULL, ADD COLUMN "review_reason" character varying NOT NULL DEFAULT '';
-- Modify "supply_mappings" table
ALTER TABLE "supply_mappings" ADD COLUMN "stock_checked_at" timestamptz NULL;
