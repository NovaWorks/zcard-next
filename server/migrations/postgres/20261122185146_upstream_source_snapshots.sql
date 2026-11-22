-- Modify "order_items" table
ALTER TABLE "order_items" ADD COLUMN "upstream_source_snapshot" jsonb NULL;
-- Modify "procurement_orders" table
ALTER TABLE "procurement_orders" ADD COLUMN "upstream_source_snapshot" jsonb NULL;
