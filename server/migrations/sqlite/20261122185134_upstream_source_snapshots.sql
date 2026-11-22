-- Add column "upstream_source_snapshot" to table: "order_items"
ALTER TABLE `order_items` ADD COLUMN `upstream_source_snapshot` json NULL;
-- Add column "upstream_source_snapshot" to table: "procurement_orders"
ALTER TABLE `procurement_orders` ADD COLUMN `upstream_source_snapshot` json NULL;
