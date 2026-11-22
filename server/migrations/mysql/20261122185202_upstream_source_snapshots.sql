-- Modify "order_items" table
ALTER TABLE `order_items` ADD COLUMN `upstream_source_snapshot` json NULL;
-- Modify "procurement_orders" table
ALTER TABLE `procurement_orders` ADD COLUMN `upstream_source_snapshot` json NULL;
