-- Modify "procurement_items" table
ALTER TABLE `procurement_items` MODIFY COLUMN `received_content` json NULL;
-- Modify "procurement_orders" table
ALTER TABLE `procurement_orders` ADD COLUMN `last_error` longtext NULL, ADD COLUMN `upstream_refund_id` varchar(80) NULL;
