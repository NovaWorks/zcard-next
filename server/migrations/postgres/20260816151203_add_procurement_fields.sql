-- Modify "procurement_items" table
ALTER TABLE "procurement_items" ALTER COLUMN "received_content" TYPE jsonb;
-- Modify "procurement_orders" table
ALTER TABLE "procurement_orders" ADD COLUMN "last_error" text NULL, ADD COLUMN "upstream_refund_id" character varying NULL;
